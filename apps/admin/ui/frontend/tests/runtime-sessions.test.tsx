import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { RuntimeSession } from '../src/api/runtime-sessions';
import { SessionsPage } from '../src/pages/SessionsPage';
import { upstreamURL } from './proxy';

const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };
const credentials = { username: 'operator', url: connection.url };
const session: RuntimeSession = {
  id: '12345678-1234-4234-8234-123456789abc', scope_id: '22345678-1234-4234-8234-123456789abc',
  owner_agent_id: 'shell-agent-12345678', kind: 'shell', label: 'Development host', state: 'connected', disposition: 'active',
  endpoint: 'ssh://host.example.test', peer: 'host.example.test:22', hostname: 'dev-host', user: 'operator',
  busy: true, revision: 3, created_at: '2026-10-01T10:00:00Z', updated_at: '2026-10-02T10:00:00Z',
  connected_at: '2026-10-01T10:01:00Z', verified_at: '2026-10-02T10:00:00Z',
};
const released: RuntimeSession = {
  ...session, id: '32345678-1234-4234-8234-123456789abc', label: 'Old resource', state: 'lost',
  disposition: 'released', release_reason: 'abandoned', busy: false, hostname: 'retired-host',
};

function deferred() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>(complete => { resolve = complete; });
  return { promise, resolve };
}

function fixture(respond?: (url: URL, init: RequestInit) => Response | Promise<Response> | undefined) {
  const state = { items: [session, released], current: session };
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = new URL(upstreamURL(input));
    const custom = respond?.(url, init);
    if (custom !== undefined) return custom;
    if (init.method === 'POST') throw new Error('Unexpected session mutation');
    if (url.pathname === '/api/v1/sessions') return Response.json({ items: state.items });
    if (url.pathname === `/api/v1/sessions/${released.id}`) return Response.json(released);
    return Response.json(state.current);
  });
  vi.stubGlobal('fetch', fetch);
  return { state, fetch, writes: () => fetch.mock.calls.filter(([, init]) => init?.method === 'POST').map(([url, init]) => [url, init!] as const) };
}

function view(selectedID?: string) {
  return render(<SessionsPage connection={connection} credentials={credentials} selectedID={selectedID}
    onSelect={selectedID ? vi.fn() : undefined} onSignIn={vi.fn()} />);
}

async function openControl(name: string) {
  fireEvent.click(await screen.findByRole('button', { name }));
  return screen.findByRole('dialog', { name });
}

describe('Sessions page', () => {
  it('lists retained resources and separates owner, resource state, disposition and activity', async () => {
    fixture();
    const onOpenAgent = vi.fn();
    render(<SessionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} onOpenAgent={onOpenAgent} />);
    expect(await screen.findByRole('button', { name: `View session ${session.id}` })).toHaveTextContent('ConnectedActiveBusy');
    expect(screen.getByRole('button', { name: `View session ${released.id}` })).toHaveTextContent('LostReleasedIdle');
    expect(screen.getByRole('heading', { name: 'Sessions', level: 1 })).toBeVisible();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'DEV-HOST' } });
    expect(screen.getByText('1 of 2 sessions')).toBeVisible();
    expect(screen.queryByRole('button', { name: `View session ${released.id}` })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: `View session ${session.id}` }));
    expect(await screen.findByRole('heading', { name: 'Saved session' })).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Sessions', level: 1 })).toHaveFocus();
    for (const label of ['Resource state', 'Lifecycle disposition', 'Activity', 'Scope', 'Endpoint', 'Peer', 'Hostname', 'User', 'Reported times']) expect(screen.getByText(label)).toBeVisible();
    expect(screen.getByText('ssh://host.example.test')).toBeVisible();
    expect(screen.getByText(new Date(session.connected_at!).toLocaleString())).toHaveAttribute('datetime', session.connected_at);
    fireEvent.click(screen.getByRole('button', { name: `View owner agent ${session.owner_agent_id}` }));
    expect(onOpenAgent).toHaveBeenCalledWith(session.owner_agent_id);
    fireEvent.click(screen.getByRole('button', { name: 'All sessions' }));
    expect(await screen.findByRole('searchbox')).toHaveValue('DEV-HOST');
  });

  it('applies intersecting scope and owner filters to the server only after submission', async () => {
    const api = fixture();
    view();
    await screen.findByRole('button', { name: `View session ${session.id}` });
    fireEvent.change(screen.getByLabelText('Scope ID'), { target: { value: session.scope_id } });
    fireEvent.change(screen.getByLabelText('Owner agent ID'), { target: { value: session.owner_agent_id } });
    expect(api.fetch).toHaveBeenCalledTimes(1);
    fireEvent.submit(screen.getByRole('form', { name: 'Filter sessions' }));
    await waitFor(() => expect(api.fetch).toHaveBeenCalledTimes(2));
    const requested = new URL(upstreamURL(api.fetch.mock.calls[1][0]));
    expect(requested.searchParams.get('scope_id')).toBe(session.scope_id);
    expect(requested.searchParams.get('owner_agent_id')).toBe(session.owner_agent_id);
    await screen.findByRole('button', { name: `View session ${session.id}` });
    fireEvent.change(screen.getByLabelText('Scope ID'), { target: { value: 'invalid-scope' } });
    fireEvent.submit(screen.getByRole('form', { name: 'Filter sessions' }));
    expect(screen.getByRole('alert')).toHaveTextContent('canonical lowercase UUID');
    expect(api.fetch).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    await waitFor(() => expect(api.fetch).toHaveBeenCalledTimes(3));
    expect(new URL(upstreamURL(api.fetch.mock.calls[2][0])).search).toBe('');
  });

  it('reads route filters and reflects history changes without overwriting their exact values', async () => {
    const api = fixture();
    const onFiltersChange = vi.fn();
    const props = { connection, credentials, onSignIn: vi.fn(), onFiltersChange };
    const page = render(<SessionsPage {...props} filters={{ scope_id: session.scope_id }} />);
    expect(await screen.findByLabelText('Scope ID')).toHaveValue(session.scope_id);
    await waitFor(() => expect(api.fetch).toHaveBeenCalledOnce());
    expect(new URL(upstreamURL(api.fetch.mock.calls[0][0])).searchParams.get('scope_id')).toBe(session.scope_id);
    page.rerender(<SessionsPage {...props} filters={{ owner_agent_id: session.owner_agent_id }} />);
    expect(await screen.findByLabelText('Scope ID')).toHaveValue('');
    expect(screen.getByLabelText('Owner agent ID')).toHaveValue(session.owner_agent_id);
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(onFiltersChange).toHaveBeenCalledWith({});
  });

  it('keeps malformed deep-link filters editable and can recover by clearing them', async () => {
    const api = fixture();
    render(<SessionsPage connection={connection} credentials={credentials} filters={{ scope_id: 'invalid-scope' }} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('unexpected session response');
    expect(screen.getByLabelText('Scope ID')).toHaveValue('invalid-scope');
    expect(api.fetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(await screen.findByRole('button', { name: `View session ${session.id}` })).toBeVisible();
    expect(screen.getByLabelText('Scope ID')).toHaveValue('');
    expect(api.writes()).toHaveLength(0);
  });

  it('keeps released identities and unassigned reservations read-only', async () => {
    const api = fixture();
    const page = view(released.id);
    expect(await screen.findByText('Abandoned; closure unconfirmed')).toBeVisible();
    for (const name of ['Inspect saved state', 'Verify', 'Close session', 'Abandon session']) expect(screen.getByRole('button', { name })).toBeDisabled();
    expect(screen.getByText('This session is retained history. Its identity and release evidence are read-only.')).toBeVisible();
    api.state.current = { ...session, owner_agent_id: '', disposition: 'reserved', state: 'starting' };
    page.rerender(<SessionsPage connection={connection} credentials={credentials} selectedID={session.id} onSelect={vi.fn()} onSignIn={vi.fn()} />);
    expect(await screen.findByText('This session has no assigned owner yet. Owner controls are unavailable.')).toBeVisible();
    for (const name of ['Inspect saved state', 'Verify', 'Close session', 'Abandon session']) expect(screen.getByRole('button', { name })).toBeDisabled();
    expect(api.writes()).toHaveLength(0);
  });

  it('requires close confirmation, sends once, and presents a closing response as unconfirmed', async () => {
    const response = deferred();
    const api = fixture((_url, init) => init.method === 'POST' ? response.promise : undefined);
    view(session.id);
    const dialog = await openControl('Close session');
    const submit = within(dialog).getByRole('button', { name: 'Close session' });
    expect(submit).toBeDisabled();
    fireEvent.submit(dialog.querySelector('form')!);
    expect(api.writes()).toHaveLength(0);
    fireEvent.click(within(dialog).getByRole('checkbox'));
    fireEvent.click(submit);
    fireEvent.submit(dialog.querySelector('form')!);
    expect(api.writes()).toHaveLength(1);
    const [input, init] = api.writes()[0];
    expect(String(input)).toMatch(/^\/ui\/proxy\?/);
    expect(JSON.parse(init.body as string)).toMatchObject({ id: session.id, owner_agent_id: session.owner_agent_id, scope_id: session.scope_id, method: 'close' });
    expect(JSON.parse(init.body as string).request_id).toMatch(/^[0-9a-f-]{36}$/);
    api.state.current = { ...session, disposition: 'closing' };
    await act(async () => response.resolve(Response.json(api.state.current)));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(await screen.findByText('Closing')).toBeVisible();
    expect(screen.getAllByText(/Close requested. Closure is not confirmed/).length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: 'Close session' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Verify' })).toBeDisabled();
    expect(api.writes()).toHaveLength(1);
  });

  it('retains an unknown request ID and reads saved state without retrying its mutation', async () => {
    const api = fixture((_url, init) => init.method === 'POST'
      ? Response.json({ error: 'session_control_uncertain', message: 'private-server-diagnostic' }, { status: 503 }) : undefined);
    view(session.id);
    const dialog = await openControl('Verify');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Verify' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('outcome of this request is unknown');
    const requestID = JSON.parse(api.writes()[0][1].body as string).request_id;
    expect(dialog).toHaveTextContent(requestID);
    expect(dialog).not.toHaveTextContent('private-server-diagnostic');
    expect(within(dialog).queryByRole('button', { name: 'Verify' })).not.toBeInTheDocument();
    api.state.current = { ...session, disposition: 'closing' };
    fireEvent.click(within(dialog).getByRole('button', { name: 'Inspect saved state' }));
    expect(await within(dialog).findByText('Saved state: connected · closing')).toBeVisible();
    await waitFor(() => expect(screen.getByRole('dialog', { name: 'Verify' })).toHaveTextContent(requestID));
    expect(api.writes()).toHaveLength(1);
    expect(within(dialog).getByText(/it has not been retried/)).toBeVisible();
  });

  it('retains the guard after dismissal and navigation until an explicit saved readback', async () => {
    const api = fixture((_url, init) => init.method === 'POST'
      ? Response.json({ error: 'session_control_uncertain' }, { status: 503 }) : undefined);
    view();
    fireEvent.click(await screen.findByRole('button', { name: `View session ${session.id}` }));
    const dialog = await openControl('Verify');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Verify' }));
    await within(dialog).findByRole('alert');
    const requestID = JSON.parse(api.writes()[0][1].body as string).request_id;
    fireEvent.click(within(dialog).getByRole('button', { name: 'Dismiss' }));
    expect(screen.getByRole('button', { name: 'Verify' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Close session' })).toBeDisabled();
    expect(screen.getByText(requestID)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'All sessions' }));
    fireEvent.click(await screen.findByRole('button', { name: `View session ${session.id}` }));
    expect(await screen.findByRole('button', { name: 'Verify' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Verify' })).toBeDisabled());
    fireEvent.click(screen.getByRole('button', { name: 'Inspect saved state' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Verify' })).toBeEnabled());
    expect(api.writes()).toHaveLength(1);
    expect(screen.queryByText(requestID)).not.toBeInTheDocument();
    expect(screen.getByText('Saved state: connected · active')).toBeVisible();
  });

  it('retains an in-flight request when waiting is cancelled and ignores its late success', async () => {
    const response = deferred();
    let requestSignal: AbortSignal | null | undefined;
    const api = fixture((_url, init) => {
      if (init.method === 'POST') { requestSignal = init.signal; return response.promise; }
      return undefined;
    });
    view(session.id);
    const dialog = await openControl('Verify');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Verify' }));
    const requestID = JSON.parse(api.writes()[0][1].body as string).request_id;
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(requestSignal?.aborted).toBe(true);
    expect(screen.getByText(requestID)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Verify' })).toBeDisabled();
    await act(async () => response.resolve(Response.json(session)));
    expect(screen.getByRole('button', { name: 'Verify' })).toBeDisabled();
    expect(screen.getByText(requestID)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Inspect saved state' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Verify' })).toBeEnabled());
    expect(api.writes()).toHaveLength(1);
  });

  it('requires explicit acceptance before abandonment and allows it for a lost closing session', async () => {
    const api = fixture((_url, init) => init.method === 'POST' ? Response.json({ ...released, id: session.id }) : undefined);
    api.state.current = { ...session, state: 'lost', disposition: 'closing' };
    view(session.id);
    const dialog = await openControl('Abandon session');
    expect(dialog).toHaveTextContent('without confirming closure');
    expect(within(dialog).getByRole('button', { name: 'Abandon session' })).toBeDisabled();
    fireEvent.click(within(dialog).getByRole('checkbox'));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Abandon session' }));
    await waitFor(() => expect(api.writes()).toHaveLength(1));
    expect(JSON.parse(api.writes()[0][1].body as string).method).toBe('abandon');
  });

  it('aborts controls on connection changes and ignores a late response from the prior connection', async () => {
    const response = deferred();
    let requestSignal: AbortSignal | null | undefined;
    fixture((url, init) => {
      if (init.method === 'POST') { requestSignal = init.signal; return response.promise; }
      if (url.origin !== connection.url) return Response.json({ ...session, label: 'Other connection resource' });
      return undefined;
    });
    const page = view(session.id);
    const dialog = await openControl('Verify');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Verify' }));
    const other = { ...connection, id: 'other', url: 'https://other.example.test' };
    page.rerender(<SessionsPage connection={other} credentials={{ ...credentials, url: other.url }} selectedID={session.id} onSelect={vi.fn()} onSignIn={vi.fn()} />);
    expect(requestSignal?.aborted).toBe(true);
    expect(await screen.findByText('Other connection resource')).toBeVisible();
    await act(async () => response.resolve(Response.json(session)));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByText('Session observation saved.')).not.toBeInTheDocument();
    expect(screen.getByText('Other connection resource')).toBeVisible();
  });

  it('polls observations every ten seconds and never submits controls while polling', async () => {
    vi.useFakeTimers();
    try {
      const api = fixture();
      await act(async () => { view(session.id); });
      const savedSession = () => within(screen.getByRole('heading', { name: 'Saved session' }).closest('section')!);
      expect(savedSession().getByText('Connected')).toBeVisible();
      api.state.current = { ...session, state: 'lost', busy: false };
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(savedSession().getByText('Lost')).toBeVisible();
      expect(api.fetch).toHaveBeenCalledTimes(2);
      expect(api.writes()).toHaveLength(0);
    } finally { vi.useRealTimers(); }
  });

  it('requires sign-in before loading sessions', () => {
    const api = fixture();
    const onSignIn = vi.fn();
    render(<SessionsPage connection={connection} onSignIn={onSignIn} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Sign in to view sessions');
    expect(api.fetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(onSignIn).toHaveBeenCalledOnce();
  });
});

import { upstreamURL } from './proxy';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Mission } from '../src/api/types';
import { MissionsPage } from '../src/pages/MissionsPage';

const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };
const credentials = { username: 'operator', url: connection.url };
const mission: Mission = {
  name: 'review-website-with-browser',
  objectives: ['Review website links.', 'Assess page quality.'],
  required_actions: ['discover-pages', 'check-links'],
  optional_actions: ['capture-page'],
  starting_actions: ['discover-pages'],
  input_schema: {
    type: 'object', required: ['url'],
    properties: { url: { type: 'string', description: 'Website to review' }, max_pages: { type: 'integer', default: 3 } },
  },
  output_schema: { type: 'object', properties: { summary: { type: 'string' } } },
  rules: {
    'aggregate-results': { for_actions: ['discover-pages'] },
    'decide-next-actions': { for_actions: ['check-links', 'capture-page'], params: { decision_by: 'model' } },
    'same-agent': { for_actions: ['discover-pages', 'check-links'] },
  },
};
const otherMission: Mission = { ...mission, name: 'review-service', objectives: ['Check service availability.'] };

function mockAPI(respond?: (url: string, init: RequestInit) => Response | Promise<Response>) {
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    if (respond) return respond(upstreamURL(input), init);
    return upstreamURL(input).endsWith('/api/v1/missions')
      ? Response.json({ items: [mission, otherMission] }) : Response.json(mission);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>(complete => { resolve = complete; });
  return { promise, resolve };
}

describe('missions browser', () => {
  it('searches missions, opens their definition and preserves the search when returning', async () => {
    const fetch = mockAPI();
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('button', { name: 'View mission review-website-with-browser' })).toBeVisible();
    expect(screen.getByText('2 missions')).toBeVisible();
    const listedMission = within(screen.getByRole('button', { name: 'View mission review-website-with-browser' }));
    expect(listedMission.getByText('2 required actions')).toBeVisible();
    expect(listedMission.getByText('1 optional action')).toBeVisible();

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search missions' }), { target: { value: 'PAGE QUALITY' } });
    expect(screen.getByText('1 of 2 missions')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'View mission review-service' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'View mission review-website-with-browser' }));

    expect(await screen.findByRole('heading', { name: 'Starting actions' })).toBeVisible();
    expect(screen.getByRole('heading', { name: mission.name, level: 1 })).toHaveFocus();
    const objectives = screen.getByRole('heading', { name: 'Objectives' }).closest('section')!;
    expect(within(objectives).getByRole('list')).toHaveClass('catalog-tags');
    expect(within(objectives).getAllByRole('listitem').map(item => item.textContent)).toEqual(mission.objectives);
    expect(screen.getByText('Website to review')).toBeVisible();
    const optionalActions = screen.getByRole('heading', { name: 'Optional actions' }).closest('section')!;
    expect(within(optionalActions).getByText('capture-page')).toBeVisible();
    expect(within(optionalActions).queryByText('check-links')).not.toBeInTheDocument();
    const rules = within(screen.getByRole('table', { name: 'Mission rules' }));
    expect(rules.getAllByRole('row')).toHaveLength(4);
    expect(rules.getByRole('row', { name: 'Aggregate results discover-pages All direct children' })).toBeVisible();
    expect(rules.getByRole('row', { name: 'Same agent discover-pages check-links One agent per operation; no fallback' })).toBeVisible();
    expect(rules.getByRole('row', { name: 'Decide next actions check-links capture-page Model' })).toBeVisible();
    expect(fetch.mock.calls.some(([url]) => upstreamURL(url) === `${connection.url}/api/v1/missions/review-website-with-browser`)).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'All missions' }));

    expect(await screen.findByRole('searchbox', { name: 'Search missions' })).toHaveValue('PAGE QUALITY');
    expect(screen.getByRole('heading', { name: 'Missions', level: 1 })).toHaveFocus();
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'not in the catalog' } });
    expect(screen.getByRole('heading', { name: 'No matching missions' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
    expect(screen.getByRole('button', { name: 'View mission review-service' })).toBeVisible();
  });

  it('renders an empty catalog and nullable definition fields without inventing data', async () => {
    const empty: Mission = {
      name: 'manual-review', objectives: ['A manual review.'], starting_actions: null, required_actions: null,
      input_schema: null, output_schema: null,
    };
    let items: Mission[] = [];
    mockAPI(url => url.endsWith('/api/v1/missions') ? Response.json({ items }) : Response.json(empty));
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('heading', { name: 'No missions yet' })).toBeVisible();
    expect(screen.queryByRole('searchbox')).not.toBeInTheDocument();

    items = [empty];
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    fireEvent.click(await screen.findByRole('button', { name: 'View mission manual-review' }));

    expect(await screen.findByText('No rules defined.')).toBeVisible();
    expect(screen.getByText('Valid proposals run automatically unless a rule requires model review. Failed work and invalid or empty proposals require model review.')).toBeVisible();
    expect(screen.queryByRole('table', { name: 'Mission rules' })).not.toBeInTheDocument();
    expect(screen.getAllByText('None specified')).toHaveLength(2);
    expect(screen.queryByRole('heading', { name: 'Optional actions' })).not.toBeInTheDocument();
    expect(screen.getAllByText('No schema specified.')).toHaveLength(2);
    expect(screen.queryByText('discover-pages')).not.toBeInTheDocument();
  });

  it('retries a failed catalog request without exposing the server error body', async () => {
    let failed = true;
    mockAPI(() => failed ? Response.json({ error: 'internal database credentials' }, { status: 503 })
      : Response.json({ items: [mission] }));
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load missions. Check the connection and try again.');
    expect(screen.queryByText('internal database credentials')).not.toBeInTheDocument();

    failed = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    expect(await screen.findByRole('button', { name: 'View mission review-website-with-browser' })).toBeVisible();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('returns to the catalog when a mission is deleted before it is opened', async () => {
    let items = [mission];
    mockAPI(url => url.endsWith('/api/v1/missions') ? Response.json({ items }) : new Response(null, { status: 404 }));
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'View mission review-website-with-browser' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('This mission is no longer available.');
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();

    items = [];
    fireEvent.click(screen.getByRole('button', { name: 'All missions' }));

    expect(await screen.findByRole('heading', { name: 'No missions yet' })).toBeVisible();
  });

  it('requests sign-in before loading missions and offers it again for rejected credentials', async () => {
    const fetch = mockAPI(() => new Response(null, { status: 401 }));
    const onSignIn = vi.fn();
    const view = render(<MissionsPage connection={connection} onSignIn={onSignIn} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Sign in to view missions on this connection.');
    expect(fetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(onSignIn).toHaveBeenCalledOnce();

    view.rerender(<MissionsPage connection={connection} credentials={credentials} onSignIn={onSignIn} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('Sign in to view missions on this connection.');
    expect(fetch).toHaveBeenCalledOnce();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeEnabled();
  });

  it('shows permission errors without presenting them as an empty catalog', async () => {
    mockAPI(() => new Response(null, { status: 403 }));
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('This connection does not have permission to view missions.');
    expect(screen.queryByRole('heading', { name: 'No missions yet' })).not.toBeInTheDocument();
  });

  it('cancels a detail request when returning and ignores its late response', async () => {
    const detail = deferredResponse();
    let signal: AbortSignal | undefined;
    mockAPI((url, init) => {
      if (url.endsWith('/api/v1/missions')) return Response.json({ items: [mission] });
      signal = init.signal ?? undefined;
      return detail.promise;
    });
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'View mission review-website-with-browser' }));
    expect(screen.getByText('Loading mission…')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'All missions' }));
    expect(signal?.aborted).toBe(true);

    await act(async () => detail.resolve(Response.json(mission)));

    expect(await screen.findByRole('button', { name: 'View mission review-website-with-browser' })).toBeVisible();
    expect(screen.queryByRole('heading', { name: 'Starting actions' })).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('does not show a previous connection’s late catalog after switching connections', async () => {
    const first = deferredResponse();
    let signal: AbortSignal | undefined;
    const nextConnection = { ...connection, id: 'other', url: 'https://other.example.test' };
    mockAPI((url, init) => {
      if (url.startsWith(connection.url)) { signal = init.signal ?? undefined; return first.promise; }
      return Response.json({ items: [otherMission] });
    });
    const view = render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);

    view.rerender(<MissionsPage connection={nextConnection} credentials={{ ...credentials, url: nextConnection.url }} onSignIn={vi.fn()} />);
    expect(signal?.aborted).toBe(true);
    expect(await screen.findByRole('button', { name: 'View mission review-service' })).toBeVisible();
    await act(async () => first.resolve(Response.json({ items: [mission] })));

    expect(screen.queryByRole('button', { name: 'View mission review-website-with-browser' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'View mission review-service' })).toBeVisible();
  });

  it('hides loaded data immediately when credentials are forgotten and cancels a pending refresh', async () => {
    const refresh = deferredResponse();
    let signal: AbortSignal | undefined;
    let calls = 0;
    mockAPI((_url, init) => {
      if (++calls === 1) return Response.json({ items: [mission] });
      signal = init.signal ?? undefined;
      return refresh.promise;
    });
    const onSignIn = vi.fn();
    const view = render(<MissionsPage connection={connection} credentials={credentials} onSignIn={onSignIn} />);
    await screen.findByRole('button', { name: 'View mission review-website-with-browser' });

    view.rerender(<MissionsPage connection={connection} onSignIn={onSignIn} />);
    expect(screen.queryByRole('button', { name: 'View mission review-website-with-browser' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeVisible();
    view.rerender(<MissionsPage connection={connection} credentials={{ ...credentials }} onSignIn={onSignIn} />);
    await waitFor(() => expect(signal).toBeDefined());
    view.rerender(<MissionsPage connection={connection} onSignIn={onSignIn} />);
    expect(signal?.aborted).toBe(true);
    await act(async () => refresh.resolve(Response.json({ items: [mission] })));

    expect(screen.queryByRole('button', { name: 'View mission review-website-with-browser' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeVisible();
    expect(calls).toBe(2);
  });

  it('focuses the definition, preserves rejected drafts and creates only after an explicit corrected submission', async () => {
    const created = { ...mission, name: 'custom-review' };
    const rejectedDraft = JSON.stringify({ ...created, optional_actions: 'capture-page' });
    const definition = JSON.stringify(created);
    let submissions = 0;
    const fetch = mockAPI((url, init) => {
      if (init.method === 'POST') return ++submissions === 1
        ? Response.json({ error: 'private-validation-details' }, { status: 400 })
        : Response.json(created, { status: 201 });
      return url.endsWith('/api/v1/missions') ? Response.json({ items: [mission] }) : Response.json(created);
    });
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    await screen.findByRole('button', { name: `View mission ${mission.name}` });
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
    const dialog = within(screen.getByRole('dialog', { name: 'Create mission' }));
    const editor = dialog.getByRole('textbox', { name: 'Mission definition (JSON)' });
    expect(editor).toHaveFocus();
    expect(JSON.parse((editor as HTMLTextAreaElement).value).objectives).toEqual(['']);
    fireEvent.change(editor, { target: { value: '{invalid json' } });
    fireEvent.click(dialog.getByRole('button', { name: 'Create mission' }));
    expect(await dialog.findByRole('alert')).toHaveTextContent('Enter valid JSON.');
    expect(submissions).toBe(0);

    fireEvent.change(editor, { target: { value: rejectedDraft } });
    fireEvent.click(dialog.getByRole('button', { name: 'Create mission' }));
    expect(await dialog.findByRole('alert')).toHaveTextContent('Check the mission definition');
    expect(editor).toHaveValue(rejectedDraft);
    expect(editor).toBeEnabled();
    expect(editor).toHaveFocus();
    expect(submissions).toBe(1);
    expect(screen.queryByText('private-validation-details')).not.toBeInTheDocument();

    fireEvent.change(editor, { target: { value: definition } });
    fireEvent.click(dialog.getByRole('button', { name: 'Create mission' }));
    expect(await screen.findByRole('heading', { name: created.name, level: 1 })).toHaveFocus();
    expect(await screen.findByRole('heading', { name: 'Starting actions' })).toBeVisible();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'POST').map(([, init]) => init?.body))
      .toEqual([rejectedDraft, definition]);
  });

  it('locks an uncertain save and checks its saved definition without repeating the write', async () => {
    const created = { ...mission, name: 'uncertain-review' };
    const fetch = mockAPI((url, init) => {
      if (init.method === 'POST') throw new TypeError('private-network-details');
      return url.endsWith('/api/v1/missions') ? Response.json({ items: [mission] }) : Response.json(created);
    });
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    await screen.findByRole('button', { name: `View mission ${mission.name}` });
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
    const dialog = within(screen.getByRole('dialog', { name: 'Create mission' }));
    const editor = dialog.getByRole('textbox', { name: 'Mission definition (JSON)' });
    fireEvent.change(editor, { target: { value: JSON.stringify(created) } });
    fireEvent.click(dialog.getByRole('button', { name: 'Create mission' }));

    expect(await dialog.findByRole('alert')).toHaveTextContent('The save result is unknown. Check the saved mission before trying again.');
    expect(editor).toBeDisabled();
    expect(dialog.getByRole('button', { name: 'Create mission' })).toBeDisabled();
    expect(screen.queryByText('private-network-details')).not.toBeInTheDocument();
    fireEvent.click(dialog.getByRole('button', { name: 'Check saved mission' }));

    expect(await screen.findByRole('heading', { name: created.name, level: 1 })).toHaveFocus();
    expect(await screen.findByRole('heading', { name: 'Starting actions' })).toBeVisible();
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
    expect(fetch.mock.calls.some(([url, init]) => init?.method === 'GET' && upstreamURL(url).endsWith(`/api/v1/missions/${created.name}`))).toBe(true);
  });

  it('discards the draft and aborts pending creation when its session or connection changes', async () => {
    const pending: ReturnType<typeof deferredResponse>[] = [];
    const signals: AbortSignal[] = [];
    mockAPI((_url, init) => {
      if (init.method !== 'POST') return Response.json({ items: [mission] });
      const response = deferredResponse();
      pending.push(response);
      signals.push(init.signal!);
      return response.promise;
    });
    const onSignIn = vi.fn();
    const view = render(<MissionsPage connection={connection} credentials={credentials} onSignIn={onSignIn} />);
    const nextConnection = { ...connection, id: 'other', url: 'https://other.example.test' };
    const sessions = [
      { connection, credentials: { ...credentials, username: 'another-operator' } },
      { connection: nextConnection, credentials: { ...credentials, url: nextConnection.url } },
    ];
    for (const [index, next] of sessions.entries()) {
      await screen.findByRole('button', { name: `View mission ${mission.name}` });
      fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
      const dialog = within(screen.getByRole('dialog', { name: 'Create mission' }));
      const editor = dialog.getByRole('textbox', { name: 'Mission definition (JSON)' });
      expect(JSON.parse((editor as HTMLTextAreaElement).value).name).toBe('');
      fireEvent.change(editor, { target: { value: JSON.stringify({ ...mission, name: 'abandoned-review' }) } });
      fireEvent.click(dialog.getByRole('button', { name: 'Create mission' }));
      expect(await dialog.findByRole('status')).toHaveTextContent('Saving mission…');

      view.rerender(<MissionsPage {...next} onSignIn={onSignIn} />);
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(signals[index].aborted).toBe(true);
      await act(async () => pending[index].resolve(Response.json({ ...mission, name: 'abandoned-review' }, { status: 201 })));

      expect(screen.getByRole('heading', { name: 'Missions', level: 1 })).toBeVisible();
      expect(screen.queryByRole('heading', { name: 'abandoned-review' })).not.toBeInTheDocument();
    }
    await screen.findByRole('button', { name: `View mission ${mission.name}` });
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
    expect(JSON.parse((screen.getByRole('textbox', { name: 'Mission definition (JSON)' }) as HTMLTextAreaElement).value).name).toBe('');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(pending).toHaveLength(2);
  });
});

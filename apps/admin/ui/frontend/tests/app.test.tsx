import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../src/App';
import { expireSession } from '../src/connections/client';
import { CONNECTIONS_STORAGE_KEY } from '../src/connections/storage';
import { HEALTH_INTERVAL_MS } from '../src/hooks/useConnections';
import { upstreamURL } from './fakes/proxy';
import { connectionLink, customLocalURL, fillConnection, localURL, mockAPI, password, persist, remoteURL, savedConnection, stored, username } from './fixtures/app';
import { deferredResponse } from './fixtures/deferred';

beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.useRealTimers();
});

describe('connection links from Playground', () => {
  const operationID = 'review-operation';
  const operation = { id: operationID, mission: { name: 'review-website', objectives: ['Review a website'], required_actions: [],
    starting_actions: [], input_schema: {}, output_schema: {} }, input: {}, status: 'completed', tasks: [], questions: [], events: [],
    progress: { task_counts: {}, total_tasks: 0, finished_tasks: 0, pending_decisions: 0, pending_questions: 0, settled: true },
    created_at: '2026-10-06T10:00:00Z', updated_at: '2026-10-06T10:00:00Z' };

  function operationAPI() {
    const api = mockAPI();
    api.change(state => { state.intercept = url => url.endsWith(`/api/v1/operations/${operationID}`)
      ? Promise.resolve(Response.json(operation)) : undefined; });
    return api;
  }

  it('opens the linked operation after entering a password for a new local connection', async () => {
    const api = operationAPI();
    window.history.replaceState(null, '', connectionLink(localURL, 'commander', operationID));
    render(<App />);
    const dialog = await screen.findByRole('dialog', { name: 'Add connection' });
    expect(window.location.hash).toBe(`#/operations/${operationID}`);
    expect(stored()).toEqual([]);
    expect(api.fetch.mock.calls.some(([input]) => upstreamURL(input).endsWith(`/operations/${operationID}`))).toBe(false);
    fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: password } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect' }));
    expect(await screen.findByRole('heading', { name: operationID, level: 1 })).toBeVisible();
    expect(await screen.findByText('review-website')).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${stored()[0]!.id}/operations/${operationID}`);
    expect(window.location.href).not.toContain(password);
  });

  it.each([true, false])('keeps the operation destination for a saved connection (signed in: %s)', async signedIn => {
    const api = operationAPI();
    persist([savedConnection]);
    if (signedIn) api.change(state => { state.sessions.set(remoteURL, { url: remoteURL, username: 'commander' }); });
    window.history.replaceState(null, '', connectionLink(remoteURL, 'commander', operationID));
    const rendered = render(<App />);
    if (!signedIn) {
      const dialog = await screen.findByRole('dialog', { name: 'Edit connection' });
      fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
      rendered.unmount();
      render(<App />);
      fireEvent.click(await screen.findByRole('button', { name: 'Sign in' }));
      fireEvent.change(screen.getByLabelText('Password'), { target: { value: password } });
      fireEvent.click(screen.getByRole('button', { name: 'Save connection' }));
    }
    expect(await screen.findByRole('heading', { name: operationID, level: 1 })).toBeVisible();
    expect(await screen.findByText('review-website')).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${savedConnection.id}/operations/${operationID}`);
    expect(stored()).toEqual([savedConnection]);
    expect(api.fetch.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(signedIn ? 0 : 1);
  });

  it('keeps an operation link after cancelling a new connection, and after reload or browser history navigation', async () => {
    operationAPI();
    window.history.replaceState(null, '', connectionLink(localURL, 'commander', operationID));
    const rendered = render(<App />);
    const dialog = await screen.findByRole('dialog', { name: 'Add connection' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    rendered.unmount();
    render(<App />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByText(`Choose or add the API connection for operation ${operationID}.`)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));
    const retry = fillConnection();
    fireEvent.click(within(retry).getByRole('button', { name: 'Connect' }));
    await screen.findByText('review-website');
    act(() => { window.history.back(); });
    await screen.findByRole('heading', { name: 'Connections' });
    expect(window.location.hash).toBe(`#/operations/${operationID}`);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    act(() => { window.history.forward(); });
    expect(await screen.findByRole('heading', { name: operationID, level: 1 })).toBeVisible();
  });

  it('requires choosing an API for a remote operation link without requesting the operation from other APIs', async () => {
    const api = operationAPI();
    const other = { id: 'other', name: 'Other API', url: 'https://other.example.test' };
    persist([savedConnection, other]);
    for (const connection of [savedConnection, other]) api.change(state => { state.sessions.set(connection.url, { url: connection.url, username: 'commander' }); });
    window.history.replaceState(null, '', `#/operations/${operationID}`);
    render(<App />);
    expect(await screen.findByText(`Choose or add the API connection for operation ${operationID}.`)).toBeVisible();
    await act(async () => {});
    expect(api.fetch.mock.calls.some(([input]) => upstreamURL(input).endsWith(`/operations/${operationID}`))).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: `Open ${savedConnection.name}` }));
    expect(await screen.findByText('review-website')).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${savedConnection.id}/operations/${operationID}`);
    expect(api.fetch.mock.calls.filter(([input]) => upstreamURL(input).endsWith(`/operations/${operationID}`))
      .every(([input]) => upstreamURL(input).startsWith(remoteURL))).toBe(true);
  });

  it('prefills a local connection and saves only after the user submits a password', async () => {
    const api = mockAPI();
    window.history.replaceState(null, '', connectionLink('http://127.0.0.1:4274', 'local-operator'));
    render(<App />);
    const dialog = await screen.findByRole('dialog', { name: 'Add connection' });
    expect(within(dialog).getByLabelText('Connection name')).toHaveValue('Local connection');
    expect(within(dialog).getByLabelText('API URL')).toHaveValue('http://127.0.0.1:4274');
    expect(within(dialog).getByLabelText('Username')).toHaveValue('local-operator');
    expect(within(dialog).getByLabelText('Password')).toHaveValue('');
    expect(within(dialog).getByLabelText('Password')).toHaveFocus();
    expect(window.location.hash).toBe('#/');
    expect(api.fetch.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false);
    expect(stored()).toEqual([]);

    fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: password } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect' }));
    expect(await screen.findByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    expect(stored()).toEqual([{ id: expect.any(String), name: 'Local connection', url: 'http://127.0.0.1:4274' }]);
    const create = api.fetch.mock.calls.find(([, options]) => options?.method === 'POST');
    expect(JSON.parse(create?.[1]?.body as string)).toEqual({ url: 'http://127.0.0.1:4274', username: 'local-operator', password });
    expect(window.location.href).not.toContain(password);
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).not.toContain(password);
    expect(api.fetch.mock.calls.every(([input]) => String(input).startsWith('/ui/'))).toBe(true);
  });

  it('waits for session restoration and opens an already signed-in connection without another login', async () => {
    const api = mockAPI();
    const restored = deferredResponse();
    persist([savedConnection]);
    api.change(state => { state.sessions.set(remoteURL, { url: remoteURL, username: 'existing-user' }); });
    api.change(state => { state.intercept = url => url === '/ui/connections' ? restored.promise : undefined; });
    window.history.replaceState(null, '', connectionLink(`${remoteURL}/`, 'suggested-user'));
    render(<App />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await act(async () => restored.resolve(Response.json({ items: [...api.snapshot().sessions.values()] })));
    expect(await screen.findByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(window.location.hash).toBe(`#/connections/${savedConnection.id}/overview`);
    expect(stored()).toEqual([savedConnection]);
    expect(api.fetch.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false);
  });

  it('reuses a signed-out connection, preserving its name and offering the link username', async () => {
    mockAPI();
    persist([savedConnection]);
    window.history.replaceState(null, '', connectionLink(remoteURL, 'local-operator'));
    render(<App />);
    const dialog = await screen.findByRole('dialog', { name: 'Edit connection' });
    expect(within(dialog).getByLabelText('Connection name')).toHaveValue(savedConnection.name);
    expect(within(dialog).getByLabelText('Username')).toHaveValue('local-operator');
    expect(within(dialog).getByLabelText('Password')).toHaveValue('');
    fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: password } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save connection' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(stored()).toEqual([savedConnection]);
    expect(window.location.hash).toBe(`#/connections/${savedConnection.id}/overview`);
  });

  it('consumes hash navigation once without reopening after cancellation or browser history changes', async () => {
    mockAPI();
    persist([savedConnection]);
    render(<App />);
    await screen.findByRole('button', { name: `Open ${savedConnection.name}` });
    fireEvent.click(screen.getByRole('button', { name: `Open ${savedConnection.name}` }));
    const overview = window.location.hash;
    act(() => { window.location.hash = connectionLink(); });
    const dialog = await screen.findByRole('dialog', { name: 'Add connection' });
    expect(window.location.hash).toBe('#/');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    act(() => { window.history.back(); });
    await waitFor(() => expect(window.location.hash).toBe(overview));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    act(() => { window.history.forward(); });
    await waitFor(() => expect(window.location.hash).toBe('#/'));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(stored()).toEqual([savedConnection]);
  });

  it('keeps an edited draft while connection health polling refreshes', async () => {
    vi.useFakeTimers();
    const api = mockAPI();
    persist([savedConnection]);
    window.history.replaceState(null, '', connectionLink(remoteURL));
    render(<App />);
    await act(async () => {});
    const dialog = screen.getByRole('dialog', { name: 'Edit connection' });
    fireEvent.change(within(dialog).getByLabelText('Connection name'), { target: { value: 'My draft' } });
    fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: password } });
    await act(async () => { await vi.advanceTimersByTimeAsync(HEALTH_INTERVAL_MS); });
    expect(within(dialog).getByLabelText('Connection name')).toHaveValue('My draft');
    expect(within(dialog).getByLabelText('Password')).toHaveValue(password);
    expect(api.fetch.mock.calls.filter(([input]) => upstreamURL(input) === `${remoteURL}/healthz`)).toHaveLength(2);
    expect(api.fetch.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false);
  });

  it('discards unknown secret fields without probing or saving the supplied API', async () => {
    const api = mockAPI();
    window.history.replaceState(null, '', `${connectionLink(remoteURL)}&password=do-not-use`);
    render(<App />);
    await waitFor(() => expect(window.location.hash).toBe('#/'));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(stored()).toEqual([]);
    expect(api.fetch.mock.calls.some(([input]) => upstreamURL(input).startsWith(remoteURL))).toBe(false);
    expect(api.fetch.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false);
    expect(api.fetch.mock.calls.some(([input]) => String(input).includes('do-not-use'))).toBe(false);
  });
});

describe('connection setup and saved metadata', () => {
  it('offers a copyable password command after entering a URL without sending credentials', async () => {
    const api = mockAPI();
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', Object.assign(Object.create(navigator), { clipboard: { writeText } }));
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));
    expect(screen.queryByText('Need a password? Run in a terminal:')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Connection name')).toHaveValue('New connection');
    expect(screen.getByLabelText('Username')).toHaveValue('commander');
    for (const label of ['API URL', 'Password']) {
      expect(screen.getByLabelText(label)).toHaveValue('');
    }
    fillConnection();

    expect(await screen.findByText('Need a password? Run in a terminal:')).toBeVisible();
    const command = 'docker compose exec api commander-api credentials';
    expect(screen.getByText(command)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Copy command' }));
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeVisible();
    expect(writeText).toHaveBeenCalledWith(command);

    const [input, options] = api.fetch.mock.calls.find(([value]) => upstreamURL(value) === `${remoteURL}/aboutz`)!;
    expect(new URL(String(input), 'http://ui.test').pathname).toBe('/ui/proxy');
    expect(new Headers(options?.headers).get('X-Commander-UI')).toBe('1');
    expect(new Headers(options?.headers).has('Authorization')).toBe(false);
    expect(options?.credentials).toBe('same-origin');
    expect(options?.body).toBeUndefined();
    expect(String(input)).not.toContain(username.trim());
    expect(String(input)).not.toContain(password.trim());
    expect(api.fetch.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false);
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).toBeNull();
  });

  it('clears the old password hint and ignores metadata that arrives after the URL changes', async () => {
    const api = mockAPI();
    const late = deferredResponse();
    const nativePlaygroundURL = 'http://localhost:24274';
    api.change(state => { state.intercept = url => {
      if (url === `${customLocalURL}/aboutz`) return late.promise;
      if (url === `${remoteURL}/aboutz`) return Promise.resolve(Response.json({ runtime: 'native', application: 'commander-api' }));
      if (url === `${nativePlaygroundURL}/aboutz`) return Promise.resolve(Response.json({ runtime: 'native', application: 'commander' }));
      return undefined;
    }; });
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    expect(await screen.findByText('docker compose exec api commander-api credentials')).toBeVisible();

    fireEvent.change(screen.getByLabelText('API URL'), { target: { value: customLocalURL } });
    expect(screen.queryByText('Need a password? Run in a terminal:')).not.toBeInTheDocument();
    await waitFor(() => expect(api.fetch.mock.calls.some(([value]) => upstreamURL(value) === `${customLocalURL}/aboutz`)).toBe(true));
    const pending = api.fetch.mock.calls.find(([value]) => upstreamURL(value) === `${customLocalURL}/aboutz`)![1]?.signal;
    fireEvent.change(screen.getByLabelText('API URL'), { target: { value: remoteURL } });
    expect(pending?.aborted).toBe(true);
    expect(await screen.findByText('./commander-api credentials')).toBeVisible();
    await act(async () => {
      late.resolve(Response.json({ runtime: 'docker', application: 'commander' }));
    });
    expect(screen.getByText('./commander-api credentials')).toBeVisible();
    expect(screen.queryByText('docker exec commander commander credentials api')).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('API URL'), { target: { value: nativePlaygroundURL } });
    expect(await screen.findByText('/credentials')).toBeVisible();
    expect(screen.getByText('Need a password? Run in Playground:')).toBeVisible();
    expect(screen.queryByText('Need a password? Run in a terminal:')).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('API URL'), { target: { value: '' } });
    expect(screen.queryByText('Need a password? Run in Playground:')).not.toBeInTheDocument();
  });

  it.each(['unavailable', 'unsupported'])('keeps connecting available when credential metadata is %s', async failure => {
    const api = mockAPI();
    api.change(state => { state.intercept = url => url.endsWith('/aboutz') ? Promise.resolve(failure === 'unavailable'
      ? Response.json({}, { status: 503 })
      : Response.json({ runtime: 'unknown', application: 'commander-api', command: 'untrusted command' })) : undefined; });
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));
    const dialog = fillConnection();
    await waitFor(() => expect(api.fetch.mock.calls.some(([value]) => upstreamURL(value) === `${remoteURL}/aboutz`)).toBe(true));
    expect(screen.queryByText('Need a password? Run in a terminal:')).not.toBeInTheDocument();
    expect(screen.queryByText('untrusted command')).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect' }));
    expect(await screen.findByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  });

  it('leaves the password command selectable when clipboard access is denied', async () => {
    mockAPI();
    const writeText = vi.fn().mockRejectedValue(new Error('Clipboard denied'));
    vi.stubGlobal('navigator', Object.assign(Object.create(navigator), { clipboard: { writeText } }));
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Copy command' }));
    expect(await screen.findByText('Select and copy the command.')).toBeVisible();
    expect(screen.getByText('docker compose exec api commander-api credentials')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Copied' })).not.toBeInTheDocument();
  });

  it('discovers the local API without sending credentials or saving a connection', async () => {
    const api = mockAPI();
    render(<App />);

    expect(await screen.findByText('Local Commander detected')).toBeVisible();
    expect(screen.getByText('0 connections')).toBeVisible();
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).toBeNull();
    expect(api.fetch.mock.calls.map(([url]) => upstreamURL(url))).toEqual(expect.arrayContaining(['/ui/connections', `${localURL}/healthz`, `${localURL}/readyz`]));
    for (const [, options] of api.fetch.mock.calls) {
      expect(new Headers(options?.headers).has('Authorization')).toBe(false);
      expect(new Headers(options?.headers).get('X-Commander-UI')).toBe('1');
      expect(options?.credentials).toBe('same-origin');
    }

    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    expect(screen.getByRole('dialog', { name: 'Add connection' })).toBeVisible();
    expect(screen.getByLabelText('API URL')).toHaveValue(localURL);
    expect(screen.getByLabelText('API URL')).toHaveAccessibleDescription("Enter the API's public URL.");
    expect(screen.getByLabelText('Password')).toHaveAttribute('type', 'password');
  });

  it('connects to the detected local API after entering only a password and suppresses saved local discovery', async () => {
    const api = mockAPI();
    const first = render(<App />);

    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    expect(screen.getByLabelText('Connection name')).toHaveValue('Local connection');
    expect(screen.getByLabelText('API URL')).toHaveValue(localURL);
    expect(screen.getByLabelText('Username')).toHaveValue('commander');
    expect(screen.getByLabelText('Password')).toHaveValue('');
    expect(screen.getByLabelText('Password')).toHaveFocus();
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: password } });
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Connect' }));
    await screen.findByRole('heading', { name: 'Overview', level: 1 });

    expect(stored()).toEqual([{ id: expect.any(String), name: 'Local connection', url: localURL }]);
    const create = api.fetch.mock.calls.find(([, options]) => options?.method === 'POST');
    expect(JSON.parse(create?.[1]?.body as string)).toEqual({ url: localURL, username: 'commander', password });
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).not.toContain(password);
    fireEvent.click(screen.getByRole('button', { name: 'All connections' }));
    expect(screen.queryByText('Local Commander detected')).not.toBeInTheDocument();

    first.unmount();
    api.fetch.mockClear();
    render(<App />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open Local connection' }))
      .toHaveAccessibleDescription(`${localURL} Connected`));
    expect(screen.queryByText('Local Commander detected')).not.toBeInTheDocument();
    expect(api.fetch.mock.calls.filter(([input]) => upstreamURL(input) === `${localURL}/healthz`)).toHaveLength(1);
  });

  it('opens manual Add with default name and username, including after cancelling detected local setup', async () => {
    mockAPI();
    render(<App />);
    await screen.findByText('Local Commander detected');

    for (const localFirst of [false, true]) {
      if (localFirst) {
        fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
        expect(screen.getByLabelText('Connection name')).toHaveValue('Local connection');
        expect(screen.getByLabelText('API URL')).toHaveValue(localURL);
        expect(screen.getByLabelText('Username')).toHaveValue('commander');
        fireEvent.change(screen.getByLabelText('Password'), { target: { value: password } });
        fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
      }
      fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));
      expect(screen.getByLabelText('Connection name')).toHaveValue('New connection');
      expect(screen.getByLabelText('Username')).toHaveValue('commander');
      for (const label of ['API URL', 'Password']) {
        expect(screen.getByLabelText(label)).toHaveValue('');
      }
      expect(screen.getByLabelText('API URL')).toHaveFocus();
      fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    }
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).toBeNull();
  });

  it('hides discovery when the default API is unavailable and allows a custom port to be entered manually', async () => {
    const api = mockAPI();
    api.change(state => { state.intercept = url => url.startsWith(localURL)
      ? Promise.resolve(Response.json({ status: 'unavailable' }, { status: 503 })) : undefined; });
    render(<App />);

    await waitFor(() => expect(screen.queryByText('Looking for a local API…')).not.toBeInTheDocument());
    expect(screen.queryByText('Local Commander detected')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Connect' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));
    expect(screen.getByLabelText('API URL')).toHaveValue('');
    fillConnection({ name: 'Custom local connection', url: customLocalURL });
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Connect' }));

    expect(await screen.findByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    expect(stored()).toEqual([{ id: expect.any(String), name: 'Custom local connection', url: customLocalURL }]);
    const create = api.fetch.mock.calls.find(([, options]) => options?.method === 'POST');
    expect(JSON.parse(create?.[1]?.body as string)).toEqual({ url: customLocalURL, username, password });
  });

  it('opens a checked connection, saves only metadata, and restores its server session after reload', async () => {
    const api = mockAPI();
    const first = render(<App />);
    await screen.findByText('Local Commander detected');
    const addButton = screen.getByRole('button', { name: 'Add connection' });
    addButton.focus();
    fireEvent.click(addButton);
    const dialog = fillConnection({ name: '  Remote Commander  ', url: ' HTTPS://API.EXAMPLE.TEST:443/commander/// ' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect' }));

    expect(await screen.findByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    await waitFor(() => expect(screen.getByRole('main')).toHaveFocus());
    expect(screen.getByText('Connected', { exact: true })).toBeVisible();
    expect(screen.getByText(remoteURL)).toBeVisible();
    const [saved] = stored();
    expect(saved).toEqual({ id: expect.any(String), name: 'Remote Commander', url: remoteURL });
    expect(Object.keys(saved).sort()).toEqual(['id', 'name', 'url']);
    const raw = localStorage.getItem(CONNECTIONS_STORAGE_KEY)!;
    expect(raw).not.toContain(username.trim());
    expect(raw).not.toContain(password);

    const authenticated = api.fetch.mock.calls.filter(([url]) => upstreamURL(url).endsWith('/api/v1/missions'));
    expect(authenticated.length).toBeGreaterThan(0);
    for (const [url, options] of authenticated) {
      expect(upstreamURL(url)).toBe(`${remoteURL}/api/v1/missions`);
      expect(new Headers(options?.headers).has('Authorization')).toBe(false);
      expect(options?.credentials).toBe('same-origin');
    }

    fireEvent.click(screen.getByRole('button', { name: 'All connections' }));
    expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Connected`);
    first.unmount();
    api.fetch.mockClear();
    render(<App />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open Remote Commander' }))
      .toHaveAccessibleDescription(`${remoteURL} Connected`));
    expect(api.fetch.mock.calls.every(([, options]) => !new Headers(options?.headers).has('Authorization'))).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Edit Remote Commander' }));
    expect(screen.getByLabelText('Connection name')).toHaveValue('Remote Commander');
    expect(screen.getByLabelText('API URL')).toHaveValue(remoteURL);
    expect(screen.getByLabelText('Username')).toHaveValue(username);
    expect(screen.getByLabelText('Password')).toHaveValue('');
    expect(screen.getByLabelText('Password')).toHaveFocus();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(stored()).toEqual([saved]);
  });

  it('keeps rejected credentials out of saved connections', async () => {
    const api = mockAPI();
    api.change(state => { state.authorize = false; });
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));
    const dialog = fillConnection();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('The API did not accept these credentials.');
    expect(screen.getByRole('dialog', { name: 'Add connection' })).toBeVisible();
    expect(within(dialog).getByRole('button', { name: 'Connect' })).toBeEnabled();
    expect(screen.queryByRole('heading', { name: 'Overview' })).not.toBeInTheDocument();
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).toBeNull();
    expect(screen.getByRole('alert').textContent).not.toContain(password);
  });

  it.each(['Operations', 'Missions', 'Controllers', 'Agents', 'Actions'])('opens %s from the sidebar and keeps that page selected after signing in', async page => {
    const resource = page.toLowerCase();
    persist([savedConnection]);
    const api = mockAPI();
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Open Remote Commander' }));
    fireEvent.click(screen.getByRole('button', { name: page }));
    expect(screen.getByRole('heading', { name: page, level: 1 })).toBeVisible();
    expect(screen.getByRole('alert')).toHaveTextContent(`Sign in to view ${resource} on this connection.`);
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(screen.getByLabelText('Connection name')).toHaveValue(savedConnection.name);
    expect(screen.getByLabelText('API URL')).toHaveValue(savedConnection.url);
    expect(screen.getByLabelText('Username')).toHaveValue('commander');
    expect(screen.getByLabelText('Password')).toHaveValue('');
    expect(screen.getByLabelText('Password')).toHaveFocus();
    const dialog = fillConnection();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save connection' }));

    expect(await screen.findByRole('heading', { name: `No ${resource} yet` })).toBeVisible();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: page })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('main')).toHaveFocus();
    expect(screen.queryByRole('heading', { name: 'Overview', level: 1 })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Overview' }));
    expect(screen.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: page }));
    expect(await screen.findByRole('heading', { name: `No ${resource} yet` })).toBeVisible();
    expect(api.fetch.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1);
  });

  it('rejects an existing URL after normalization without testing or saving it again', async () => {
    persist([savedConnection]);
    const api = mockAPI();
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));
    const dialog = fillConnection({ name: 'Duplicate', url: 'https://API.EXAMPLE.TEST:443/commander/' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('A connection with this API URL already exists.');
    expect(stored()).toEqual([savedConnection]);
    expect(api.fetch.mock.calls.some(([url]) => upstreamURL(url).endsWith('/api/v1/missions'))).toBe(false);
  });

  it('edits metadata in place and removes the active connection without removing other connections', async () => {
    const other = { id: 'other', name: 'Other Commander', url: 'https://other.example.test' };
    persist([savedConnection, other]);
    mockAPI();
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Edit Remote Commander' }));
    expect(screen.getByLabelText('Connection name')).toHaveValue(savedConnection.name);
    expect(screen.getByLabelText('API URL')).toHaveValue(savedConnection.url);
    expect(screen.getByLabelText('Username')).toHaveValue('commander');
    expect(screen.getByLabelText('Password')).toHaveValue('');
    const dialog = fillConnection({ name: 'Renamed Commander', url: 'https://new.example.test/api-root/' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save connection' }));

    await screen.findByRole('heading', { name: 'Overview', level: 1 });
    expect(screen.getByRole('main')).toHaveFocus();
    expect(stored()).toEqual([{ id: savedConnection.id, name: 'Renamed Commander', url: 'https://new.example.test/api-root' }, other]);
    fireEvent.click(screen.getByRole('button', { name: 'Connection settings' }));
    expect(screen.getByText('Signed in through the UI server')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Remove connection' }));
    const confirmation = screen.getByRole('dialog', { name: 'Remove connection?' });
    expect(confirmation).toHaveAccessibleDescription('Remove Renamed Commander from this browser? This does not delete any Commander data.');
    fireEvent.click(within(confirmation).getByRole('button', { name: 'Remove connection' }));

    await waitFor(() => expect(screen.getByRole('heading', { name: 'Connections' })).toHaveFocus());
    expect(screen.queryByRole('button', { name: 'Open Renamed Commander' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open Other Commander' })).toBeVisible();
    expect(stored()).toEqual([other]);
  });

  it('warns when browser storage cannot save while keeping the connection usable for this tab', async () => {
    mockAPI();
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('Storage unavailable', 'QuotaExceededError'); });
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));
    const dialog = fillConnection();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect' }));

    await screen.findByRole('heading', { name: 'Overview', level: 1 });
    expect(screen.getByRole('alert')).toHaveTextContent('This browser could not save your connections. Changes will last only for this tab.');
    expect(screen.getByText('Connected', { exact: true })).toBeVisible();
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'All connections' }));
    expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toBeVisible();
  });
});

describe('in-flight connection checks', () => {
  it('revalidates a stale 401 against the current session and expires only a missing API', async () => {
    const other = { id: 'other', name: 'Other Commander', url: localURL };
    persist([savedConnection, other]);
    const api = mockAPI();
    api.change(state => { state.sessions.set(remoteURL, { url: remoteURL, username }); });
    api.change(state => { state.sessions.set(localURL, { url: localURL, username: 'another-operator' }); });
    render(<App />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Connected`));
    await act(async () => { expireSession(remoteURL); });
    expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Connected`);
    expect(screen.getByRole('button', { name: 'Open Other Commander' })).toHaveAccessibleDescription(`${localURL} Connected`);

    api.change(state => { state.sessions.delete(remoteURL); });
    await act(async () => { expireSession(remoteURL); });
    expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Sign in required`);
    expect(screen.getByRole('button', { name: 'Open Other Commander' })).toHaveAccessibleDescription(`${localURL} Connected`);
    expect(stored()).toEqual([savedConnection, other]);
    expect(api.fetch.mock.calls.filter(([, options]) => options?.method === 'DELETE')).toHaveLength(0);
  });

  it('retains a known session when revalidation fails and clears the warning after recovery', async () => {
    persist([savedConnection]);
    const api = mockAPI();
    api.change(state => { state.sessions.set(remoteURL, { url: remoteURL, username }); });
    render(<App />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Connected`));
    api.change(state => { state.intercept = url => url === '/ui/connections' ? Promise.resolve(Response.json({}, { status: 502 })) : undefined; });
    await act(async () => { expireSession(remoteURL); });
    expect(screen.getByRole('alert')).toHaveTextContent('Could not restore sign-ins');
    expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Connected`);
    api.change(state => { state.intercept = undefined; });
    await act(async () => { expireSession(remoteURL); });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Connected`);
  });

  it('ignores an old empty session response after signing in again', async () => {
    persist([savedConnection]);
    const api = mockAPI();
    api.change(state => { state.sessions.set(remoteURL, { url: remoteURL, username }); });
    render(<App />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Connected`));
    const delayed = deferredResponse();
    api.change(state => { state.intercept = (url, options) => url === '/ui/connections' && !options.method ? delayed.promise : undefined; });
    act(() => { expireSession(remoteURL); });
    fireEvent.click(screen.getByRole('button', { name: 'Edit Remote Commander' }));
    const dialog = fillConnection();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save connection' }));
    await screen.findByRole('heading', { name: 'Overview', level: 1 });
    await act(async () => { delayed.resolve(Response.json({ items: [] })); });
    fireEvent.click(screen.getByRole('button', { name: 'Connection settings' }));
    expect(screen.getByText('Signed in through the UI server')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeEnabled();
  });

  it('restores two API sessions and signs out only the selected connection', async () => {
    const other = { id: 'other', name: 'Other Commander', url: localURL };
    persist([savedConnection, other]);
    const api = mockAPI();
    api.change(state => { state.sessions.set(remoteURL, { url: remoteURL, username }); });
    api.change(state => { state.sessions.set(localURL, { url: localURL, username: 'another-operator' }); });
    const first = render(<App />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Connected`));
    expect(screen.getByRole('button', { name: 'Open Other Commander' })).toHaveAccessibleDescription(`${localURL} Connected`);
    fireEvent.click(screen.getByRole('button', { name: 'Open Remote Commander' }));
    fireEvent.click(screen.getByRole('button', { name: 'Connection settings' }));
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(await screen.findByText('Not signed in')).toBeVisible();
    expect(api.snapshot().sessions.has(remoteURL)).toBe(false);
    expect(api.snapshot().sessions.get(localURL)?.username).toBe('another-operator');
    first.unmount();
    window.history.replaceState(null, '', '/');
    render(<App />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Sign in required`));
    expect(screen.getByRole('button', { name: 'Open Other Commander' })).toHaveAccessibleDescription(`${localURL} Connected`);
    expect(stored()).toEqual([savedConnection, other]);
  });

  it.each(['sign out', 'remove'])('keeps the connection signed in when %s fails', async command => {
    persist([savedConnection]);
    const api = mockAPI();
    api.change(state => { state.sessions.set(remoteURL, { url: remoteURL, username }); });
    api.change(state => { state.rejectDelete = true; });
    render(<App />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Connected`));
    fireEvent.click(screen.getByRole('button', { name: 'Open Remote Commander' }));
    fireEvent.click(screen.getByRole('button', { name: 'Connection settings' }));
    if (command === 'sign out') fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    else {
      fireEvent.click(screen.getByRole('button', { name: 'Remove connection' }));
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Remove connection' }));
    }
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not end this');
    expect(api.snapshot().sessions.has(remoteURL)).toBe(true);
    expect(stored()).toEqual([savedConnection]);
    expect(screen.getByText('Signed in through the UI server')).toBeVisible();
  });

  it('ends the server session and preserves focus while public health checks are pending', async () => {
    const api = mockAPI();
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));
    const dialog = fillConnection();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect' }));
    await screen.findByRole('button', { name: 'Refresh' });
    expect(screen.getByText('Connected', { exact: true })).toBeVisible();
    const metadata = stored();

    const health = deferredResponse();
    const readiness = deferredResponse();
    api.change(state => { state.intercept = url => {
      if (url === `${remoteURL}/healthz`) return health.promise;
      if (url === `${remoteURL}/readyz`) return readiness.promise;
    }; });
    api.fetch.mockClear();
    fireEvent.click(screen.getByRole('button', { name: 'Connection settings' }));
    const forgetButton = screen.getByRole('button', { name: 'Sign out' });
    forgetButton.focus();
    fireEvent.click(forgetButton);

    await waitFor(() => expect(screen.getByRole('button', { name: 'Edit connection' })).toHaveFocus());
    expect(screen.queryByRole('button', { name: 'Sign out' })).not.toBeInTheDocument();
    expect(screen.getByText('Not signed in')).toBeVisible();
    expect(api.fetch.mock.calls.map(([url]) => upstreamURL(url))).toEqual(expect.arrayContaining([`${remoteURL}/healthz`, `${remoteURL}/readyz`]));
    expect(api.snapshot().sessions.has(remoteURL)).toBe(false);
    for (const [, options] of api.fetch.mock.calls) expect(new Headers(options?.headers).has('Authorization')).toBe(false);

    fireEvent.click(screen.getByRole('button', { name: 'Overview' }));
    expect(screen.getByText('Sign in required', { exact: true })).toBeVisible();
    expect(screen.queryByText('Connected', { exact: true })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Checking…' })).toBeDisabled();
    expect(stored()).toEqual(metadata);

    await act(async () => {
      health.resolve(Response.json({ status: 'ok' }));
      readiness.resolve(Response.json({ status: 'ok' }));
    });
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled();
    expect(screen.getByText('Sign in required', { exact: true })).toBeVisible();
  });

  it('aborts cancelled setup and ignores a successful response arriving afterward', async () => {
    const api = mockAPI();
    const delayed = deferredResponse();
    let signal: AbortSignal | undefined;
    api.change(state => { state.intercept = (url, options) => {
      if (url === '/ui/connections' && options.method === 'POST') {
        signal = options.signal ?? undefined;
        return delayed.promise;
      }
    }; });
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));
    const dialog = fillConnection();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect' }));
    expect(within(dialog).getByRole('button', { name: 'Testing connection' })).toBeDisabled();
    expect(within(dialog).getByLabelText('Password')).toBeDisabled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    expect(signal?.aborted).toBe(true);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await act(async () => delayed.resolve(Response.json({ url: remoteURL, username })));
    expect(screen.queryByRole('heading', { name: 'Overview' })).not.toBeInTheDocument();
    expect(screen.getByText('0 connections')).toBeVisible();
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).toBeNull();
  });

  it('refreshes health on the interval and ignores a removed connection’s pending poll', async () => {
    vi.useFakeTimers();
    persist([savedConnection]);
    const api = mockAPI();
    render(<App />);
    await act(async () => {});
    expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Sign in required`);

    api.change(state => { state.online = false; });
    await act(async () => { await vi.advanceTimersByTimeAsync(HEALTH_INTERVAL_MS); });
    expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Unreachable`);

    api.change(state => { state.online = true; });
    const delayed = deferredResponse();
    let signal: AbortSignal | undefined;
    api.change(state => { state.intercept = (url, options) => {
      if (url === `${remoteURL}/healthz`) {
        signal = options.signal ?? undefined;
        return delayed.promise;
      }
    }; });
    await act(async () => { await vi.advanceTimersByTimeAsync(HEALTH_INTERVAL_MS); });
    expect(signal?.aborted).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Remove Remote Commander' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Remove connection' }));
    await act(async () => {});
    expect(signal?.aborted).toBe(true);
    expect(screen.getByRole('heading', { name: 'Connections' })).toHaveFocus();
    const requestCount = api.fetch.mock.calls.filter(([url]) => upstreamURL(url).startsWith(remoteURL)).length;

    await act(async () => delayed.resolve(Response.json({ status: 'ok' })));
    await act(async () => { await vi.advanceTimersByTimeAsync(HEALTH_INTERVAL_MS); });
    expect(screen.queryByRole('button', { name: 'Open Remote Commander' })).not.toBeInTheDocument();
    expect(screen.getByText('0 connections')).toBeVisible();
    expect(stored()).toEqual([]);
    expect(api.fetch.mock.calls.filter(([url]) => upstreamURL(url).startsWith(remoteURL))).toHaveLength(requestCount);
  });
});

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../src/App';
import { CONNECTIONS_STORAGE_KEY } from '../src/connections/storage';
import type { Connection } from '../src/connections/types';
import { HEALTH_INTERVAL_MS } from '../src/hooks/useConnections';

const localURL = 'http://localhost:4274';
const remoteURL = 'https://api.example.test/commander';
const username = ' operator ';
const password = ' exact secret ';
const authorization = `Basic ${btoa(`${username}:${password}`)}`;
const savedConnection = { id: 'existing', name: 'Remote Commander', url: remoteURL };

function mockAPI() {
  const state: {
    online: boolean;
    authorize: boolean;
    intercept?: (url: string, init: RequestInit) => Promise<Response> | undefined;
  } = { online: true, authorize: true };
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> => {
    const url = String(input);
    const intercepted = state.intercept?.(url, init);
    if (intercepted) return intercepted;
    if (!state.online && !url.startsWith(localURL)) throw new TypeError('Network unavailable');
    if (url.endsWith('/healthz') || url.endsWith('/readyz')) return Response.json({ status: 'ok' });
    if (url.endsWith('/api/v1/missions')) {
      return state.authorize && new Headers(init.headers).get('Authorization') === authorization
        ? Response.json({ items: [] })
        : Response.json({ error: 'authentication_required' }, { status: 401 });
    }
    throw new Error(`Unexpected test endpoint: ${url}`);
  });
  vi.stubGlobal('fetch', fetch);
  return { fetch, state };
}

function persist(connections: Connection[]) {
  localStorage.setItem(CONNECTIONS_STORAGE_KEY, JSON.stringify(connections));
}

function stored(): Connection[] {
  return JSON.parse(localStorage.getItem(CONNECTIONS_STORAGE_KEY) ?? '[]') as Connection[];
}

function fillConnection({ name = 'Remote Commander', url = remoteURL } = {}) {
  const dialog = screen.getByRole('dialog');
  fireEvent.change(within(dialog).getByLabelText('Connection name'), { target: { value: name } });
  fireEvent.change(within(dialog).getByLabelText('API URL'), { target: { value: url } });
  fireEvent.change(within(dialog).getByLabelText('Username'), { target: { value: username } });
  fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: password } });
  return dialog;
}

function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>(complete => { resolve = complete; });
  return { promise, resolve };
}

beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.useRealTimers();
});

describe('connection setup and saved metadata', () => {
  it('discovers the local API without sending credentials or saving a connection', async () => {
    const api = mockAPI();
    render(<App />);

    expect(await screen.findByText('Local Commander detected')).toBeVisible();
    expect(screen.getByText('0 connections')).toBeVisible();
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).toBeNull();
    expect(api.fetch.mock.calls.map(([url]) => url)).toEqual([`${localURL}/healthz`, `${localURL}/readyz`]);
    for (const [, options] of api.fetch.mock.calls) {
      expect(new Headers(options?.headers).has('Authorization')).toBe(false);
      expect(options?.credentials).toBe('omit');
    }

    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    expect(screen.getByRole('dialog', { name: 'Add connection' })).toBeVisible();
    expect(screen.getByLabelText('API URL')).toHaveValue(localURL);
    expect(screen.getByLabelText('Password')).toHaveAttribute('type', 'password');
  });

  it('opens a checked connection, saves only metadata, and requires sign-in after reload', async () => {
    const api = mockAPI();
    const first = render(<App />);
    await screen.findByText('Local Commander detected');
    const addButton = screen.getByRole('button', { name: 'Add connection' });
    addButton.focus();
    fireEvent.click(addButton);
    const dialog = fillConnection({ name: '  Remote Commander  ', url: ' HTTPS://API.EXAMPLE.TEST:443/commander/// ' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect' }));

    expect(await screen.findByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    expect(screen.getByRole('main')).toHaveFocus();
    expect(screen.getByText('Connected', { exact: true })).toBeVisible();
    expect(screen.getByText(remoteURL)).toBeVisible();
    const [saved] = stored();
    expect(saved).toEqual({ id: expect.any(String), name: 'Remote Commander', url: remoteURL });
    expect(Object.keys(saved).sort()).toEqual(['id', 'name', 'url']);
    const raw = localStorage.getItem(CONNECTIONS_STORAGE_KEY)!;
    expect(raw).not.toContain(username.trim());
    expect(raw).not.toContain(password);
    expect(raw).not.toContain(authorization);
    const authenticated = api.fetch.mock.calls.filter(([url]) => String(url).endsWith('/api/v1/missions'));
    expect(authenticated.length).toBeGreaterThan(0);
    for (const [url, options] of authenticated) {
      expect(url).toBe(`${remoteURL}/api/v1/missions`);
      expect(new Headers(options?.headers).get('Authorization')).toBe(authorization);
      expect(options?.credentials).toBe('omit');
    }

    fireEvent.click(screen.getByRole('button', { name: 'All connections' }));
    expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Connected`);
    first.unmount();
    api.fetch.mockClear();
    render(<App />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open Remote Commander' }))
      .toHaveAccessibleDescription(`${remoteURL} Sign in required`));
    expect(api.fetch.mock.calls.every(([, options]) => !new Headers(options?.headers).has('Authorization'))).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Open Remote Commander' }));
    const signInButton = screen.getByRole('button', { name: 'Sign in' });
    signInButton.focus();
    fireEvent.click(signInButton);
    expect(screen.getByRole('dialog', { name: 'Edit connection' })).toBeVisible();
    expect(screen.getByLabelText('Username')).toHaveValue('');
    expect(screen.getByLabelText('Password')).toHaveValue('');
    const signIn = fillConnection();
    fireEvent.click(within(signIn).getByRole('button', { name: 'Save connection' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.getByRole('main')).toHaveFocus();
    expect(screen.getByText('Connected', { exact: true })).toBeVisible();
    expect(stored()).toEqual([saved]);
  });

  it('keeps rejected credentials out of saved connections', async () => {
    const api = mockAPI();
    api.state.authorize = false;
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

  it('opens Missions from the sidebar and keeps that page selected after signing in', async () => {
    persist([savedConnection]);
    mockAPI();
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Open Remote Commander' }));
    fireEvent.click(screen.getByRole('button', { name: 'Missions' }));
    expect(screen.getByRole('heading', { name: 'Missions', level: 1 })).toBeVisible();
    expect(screen.getByRole('alert')).toHaveTextContent('Sign in to view missions on this connection.');
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    const dialog = fillConnection();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save connection' }));

    expect(await screen.findByRole('heading', { name: 'No missions yet' })).toBeVisible();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Missions' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('main')).toHaveFocus();
    expect(screen.queryByRole('heading', { name: 'Overview', level: 1 })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Overview' }));
    expect(screen.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Missions' }));
    expect(await screen.findByRole('heading', { name: 'No missions yet' })).toBeVisible();
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
    expect(api.fetch.mock.calls.some(([url]) => String(url).endsWith('/api/v1/missions'))).toBe(false);
  });

  it('edits metadata in place and removes the active connection without removing other connections', async () => {
    const other = { id: 'other', name: 'Other Commander', url: 'https://other.example.test' };
    persist([savedConnection, other]);
    mockAPI();
    render(<App />);
    await screen.findByText('Local Commander detected');
    fireEvent.click(screen.getByRole('button', { name: 'Edit Remote Commander' }));
    const dialog = fillConnection({ name: 'Renamed Commander', url: 'https://new.example.test/api-root/' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save connection' }));

    await screen.findByRole('heading', { name: 'Overview', level: 1 });
    expect(screen.getByRole('main')).toHaveFocus();
    expect(stored()).toEqual([{ id: savedConnection.id, name: 'Renamed Commander', url: 'https://new.example.test/api-root' }, other]);
    fireEvent.click(screen.getByRole('button', { name: 'Connection settings' }));
    expect(screen.getByText('Available in this tab')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Remove connection' }));
    const confirmation = screen.getByRole('dialog', { name: 'Remove connection?' });
    expect(confirmation).toHaveAccessibleDescription('Remove Renamed Commander from this browser? This does not delete any Commander data.');
    fireEvent.click(within(confirmation).getByRole('button', { name: 'Remove connection' }));

    expect(screen.getByRole('heading', { name: 'Connections' })).toHaveFocus();
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
  it('forgets credentials immediately and preserves focus while public health checks are pending', async () => {
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
    api.state.intercept = url => {
      if (url === `${remoteURL}/healthz`) return health.promise;
      if (url === `${remoteURL}/readyz`) return readiness.promise;
    };
    api.fetch.mockClear();
    fireEvent.click(screen.getByRole('button', { name: 'Connection settings' }));
    const forgetButton = screen.getByRole('button', { name: 'Forget credentials' });
    forgetButton.focus();
    fireEvent.click(forgetButton);

    expect(screen.getByRole('button', { name: 'Edit connection' })).toHaveFocus();
    expect(screen.queryByRole('button', { name: 'Forget credentials' })).not.toBeInTheDocument();
    expect(screen.getByText('Not signed in')).toBeVisible();
    expect(api.fetch.mock.calls.map(([url]) => url)).toEqual([`${remoteURL}/healthz`, `${remoteURL}/readyz`]);
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
    api.state.intercept = (url, options) => {
      if (url.endsWith('/api/v1/missions')) {
        signal = options.signal ?? undefined;
        return delayed.promise;
      }
    };
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
    await act(async () => delayed.resolve(Response.json({ items: [] })));
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

    api.state.online = false;
    await act(async () => { await vi.advanceTimersByTimeAsync(HEALTH_INTERVAL_MS); });
    expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription(`${remoteURL} Unreachable`);

    api.state.online = true;
    const delayed = deferredResponse();
    let signal: AbortSignal | undefined;
    api.state.intercept = (url, options) => {
      if (url === `${remoteURL}/healthz`) {
        signal = options.signal ?? undefined;
        return delayed.promise;
      }
    };
    await act(async () => { await vi.advanceTimersByTimeAsync(HEALTH_INTERVAL_MS); });
    expect(signal?.aborted).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Remove Remote Commander' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Remove connection' }));
    expect(signal?.aborted).toBe(true);
    expect(screen.getByRole('heading', { name: 'Connections' })).toHaveFocus();
    const requestCount = api.fetch.mock.calls.filter(([url]) => String(url).startsWith(remoteURL)).length;

    await act(async () => delayed.resolve(Response.json({ status: 'ok' })));
    await act(async () => { await vi.advanceTimersByTimeAsync(HEALTH_INTERVAL_MS); });
    expect(screen.queryByRole('button', { name: 'Open Remote Commander' })).not.toBeInTheDocument();
    expect(screen.getByText('0 connections')).toBeVisible();
    expect(stored()).toEqual([]);
    expect(api.fetch.mock.calls.filter(([url]) => String(url).startsWith(remoteURL))).toHaveLength(requestCount);
  });
});

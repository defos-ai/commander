import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Controller } from '../src/api/types';
import { ControllersPage } from '../src/pages/ControllersPage';
import { upstreamURL } from './proxy';

const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };
const credentials = { username: 'operator', url: connection.url };
const controller: Controller = {
  id: 'default-controller-0123abcd', type: 'default', status: 'ready', stale: false,
  server: { hostname: 'control-west-1', os: 'linux', arch: 'amd64' }, version: '0.1.0', agent_selector: 'round-robin',
  rule_selector: 'mission-rules',
  started_at: '2026-10-06T09:00:00Z', last_heartbeat: '2026-10-06T09:10:00Z', lease_expires_at: '2026-10-06T09:10:45Z',
};
const stale: Controller = { ...controller, id: 'custom-controller-4567efab', type: 'custom', status: 'unavailable', stale: true,
  status_reason: 'Heartbeat lease expired', server: { ...controller.server, hostname: 'control-east-1' } };

function mockAPI(respond?: (url: string, init?: RequestInit) => Response | Promise<Response>) {
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => respond ? respond(upstreamURL(input), init)
    : upstreamURL(input).endsWith('/api/v1/controllers') ? Response.json({ items: [controller, stale] }) : Response.json(controller));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

describe('controllers browser', () => {
  it('searches registered snapshots and opens host, policy and heartbeat details', async () => {
    mockAPI();
    render(<ControllersPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    const ready = await screen.findByRole('button', { name: `View controller ${controller.id}` });
    const unavailable = screen.getByRole('button', { name: `View controller ${stale.id}` });
    expect(within(ready).getByText('Ready')).toBeVisible();
    expect(within(unavailable).getByText('Unavailable')).toBeVisible();
    expect(within(unavailable).getByText('Stale')).toBeVisible();
    expect(ready.querySelector('time')).toHaveAttribute('datetime', controller.last_heartbeat);
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search controllers' }), { target: { value: 'CONTROL-WEST' } });
    expect(screen.getByText('1 of 2 controllers')).toBeVisible();
    expect(screen.queryByRole('button', { name: `View controller ${stale.id}` })).not.toBeInTheDocument();
    fireEvent.click(ready);
    expect(await screen.findByRole('heading', { name: 'Controller snapshot' })).toBeVisible();
    expect(screen.getByRole('heading', { name: controller.id, level: 1 })).toHaveFocus();
    for (const value of ['default', 'control-west-1', 'linux', 'amd64', '0.1.0', 'round-robin', 'Rule selector', 'mission-rules', 'Started at', 'Last heartbeat', 'Lease expires at']) {
      expect(screen.getByText(value)).toBeVisible();
    }
    expect(screen.getByText(new Date(controller.lease_expires_at).toLocaleString())).toHaveAttribute('datetime', controller.lease_expires_at);
    fireEvent.click(screen.getByRole('button', { name: 'All controllers' }));
    expect(await screen.findByRole('searchbox', { name: 'Search controllers' })).toHaveValue('CONTROL-WEST');
    expect(screen.getByRole('heading', { name: 'Controllers', level: 1 })).toHaveFocus();
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'absent-host' } });
    expect(screen.getByRole('heading', { name: 'No matching controllers' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
    expect(screen.getByText('2 controllers')).toBeVisible();
  });

  it('shows stale reasons and preserves future status values', async () => {
    mockAPI(() => Response.json({ ...stale, status: 'recovering' }));
    render(<ControllersPage connection={connection} credentials={credentials} selectedID={stale.id} onSelect={vi.fn()} onSignIn={vi.fn()} />);
    expect(await screen.findByText('This controller’s heartbeat lease has expired. Its reported state may have changed.')).toBeVisible();
    expect(screen.getByText('Heartbeat lease expired')).toBeVisible();
    expect(screen.getByText('recovering')).toBeVisible();
    expect(screen.getByText('Stale')).toBeVisible();
  });

  it('polls every ten seconds, removes failed snapshots and supports manual refresh', async () => {
    vi.useFakeTimers();
    try {
      let respond = () => Response.json({ items: [controller] });
      const fetch = mockAPI(() => respond());
      await act(async () => { render(<ControllersPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />); });
      expect(screen.getByText('Ready')).toBeVisible();
      respond = () => Response.json({ items: [{ ...controller, status: 'degraded' }] });
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(fetch).toHaveBeenCalledTimes(2);
      expect(screen.getByText('Degraded')).toBeVisible();
      respond = () => Response.json({ error: 'private failure detail' }, { status: 503 });
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(screen.getByRole('alert')).toHaveTextContent('Could not load controllers.');
      expect(screen.queryByText('Degraded')).not.toBeInTheDocument();
      expect(screen.queryByText('private failure detail')).not.toBeInTheDocument();
      respond = () => Response.json({ items: [controller] });
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
      await act(async () => {});
      expect(screen.getByText('Ready')).toBeVisible();
    } finally { vi.useRealTimers(); }
  });

  it('isolates pending snapshots when the connection changes or the user signs out', async () => {
    let resolve!: (response: Response) => void;
    let requestSignal: AbortSignal | null | undefined;
    const delayed = new Promise<Response>(complete => { resolve = complete; });
    const otherConnection = { ...connection, url: 'https://other.example.test' };
    const otherCredentials = { ...credentials, url: otherConnection.url };
    const other = { ...controller, id: 'other-controller', server: { ...controller.server, hostname: 'other-host' } };
    mockAPI((url, init) => {
      if (url.startsWith(connection.url)) { requestSignal = init?.signal; return delayed; }
      return Response.json({ items: [other] });
    });
    const view = render(<ControllersPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    view.rerender(<ControllersPage connection={otherConnection} credentials={otherCredentials} onSignIn={vi.fn()} />);
    expect(requestSignal?.aborted).toBe(true);
    expect(await screen.findByRole('button', { name: 'View controller other-controller' })).toBeVisible();
    await act(async () => resolve(Response.json({ items: [controller] })));
    expect(screen.queryByRole('button', { name: `View controller ${controller.id}` })).not.toBeInTheDocument();
    view.rerender(<ControllersPage connection={otherConnection} onSignIn={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'View controller other-controller' })).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Sign in to view controllers');
  });

  it('requires sign-in and stops polling after rejected credentials', async () => {
    vi.useFakeTimers();
    try {
      const fetch = mockAPI(() => new Response(null, { status: 401 }));
      const onSignIn = vi.fn();
      const view = render(<ControllersPage connection={connection} onSignIn={onSignIn} />);
      expect(fetch).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
      expect(onSignIn).toHaveBeenCalledOnce();
      await act(async () => { view.rerender(<ControllersPage connection={connection} credentials={credentials} onSignIn={onSignIn} />); });
      expect(screen.getByRole('alert')).toHaveTextContent('Sign in to view controllers');
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
      expect(fetch).toHaveBeenCalledOnce();
    } finally { vi.useRealTimers(); }
  });

  it('returns to an empty catalog after a controller detail disappears', async () => {
    let items = [controller];
    mockAPI(url => url.endsWith('/api/v1/controllers') ? Response.json({ items }) : new Response(null, { status: 404 }));
    render(<ControllersPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: `View controller ${controller.id}` }));
    expect(await screen.findByRole('alert')).toHaveTextContent('This controller is no longer available.');
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
    items = [];
    fireEvent.click(screen.getByRole('button', { name: 'All controllers' }));
    expect(await screen.findByRole('heading', { name: 'No controllers yet' })).toBeVisible();
  });
});

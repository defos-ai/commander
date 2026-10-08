import { upstreamURL } from './proxy';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../src/App';
import { CONNECTIONS_STORAGE_KEY } from '../src/connections/storage';
import { parseRoute, routeHash, type Route } from '../src/navigation/routes';

const connection = { id: 'saved-local', name: 'Local Commander', url: 'http://localhost:4274' };
const mission = { name: 'review-website', objective: 'Review website quality.', required_actions: ['discover-pages'],
  starting_actions: ['discover-pages'], input_schema: {}, output_schema: {} };
const operation = { id: 'operation-review', mission, input: { url: 'https://defos.ai' }, status: 'running',
  tasks: [], questions: [], events: [], created_at: '2026-10-06T10:00:00Z', updated_at: '2026-10-06T10:00:00Z' };

function mockAPI() {
  let session: { url: string; username: string } | undefined;
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = upstreamURL(input);
    if (url === '/ui/connections') {
      if (init.method === 'POST') { const data = JSON.parse(init.body as string); session = { url: data.url, username: data.username }; return Response.json(session); }
      return Response.json({ items: session ? [session] : [] });
    }
    if (url.endsWith('/healthz') || url.endsWith('/readyz')) return Response.json({ status: 'ok' });
    if (!session) return new Response(null, { status: 401 });
    if (url.endsWith('/missions')) return Response.json({ items: [mission] });
    if (url.endsWith('/operations')) return Response.json({ items: [operation] });
    if (url.endsWith(`/operations/${operation.id}`)) return Response.json(operation);
    if (url.endsWith('/graph')) return Response.json({ operation_id: operation.id, status: operation.status, updated_at: operation.updated_at, nodes: [], edges: [] });
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

async function signIn() {
  fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
  const dialog = screen.getByRole('dialog', { name: 'Edit connection' });
  fireEvent.change(within(dialog).getByLabelText('Username'), { target: { value: 'operator' } });
  fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: 'private-password' } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Save connection' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
}

function visit(route: Route) { window.history.replaceState(null, '', routeHash(route)); }

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem(CONNECTIONS_STORAGE_KEY, JSON.stringify([connection]));
});

describe('location navigation', () => {
  it('round-trips entity names and IDs without treating their punctuation as route segments', () => {
    const routes: Route[] = [{}, { connectionID: 'local', section: 'overview' }, { connectionID: 'local', section: 'settings' },
      ...(['missions', 'controllers', 'actions', 'agents', 'operations'] as const).map(section => ({ connectionID: 'private/team', section, itemID: 'Résumé / a?#b' })),
      { connectionID: 'local', section: 'operations', itemID: operation.id, view: 'graph' }];

    for (const route of routes) expect(parseRoute(routeHash(route))).toEqual(route);
    expect(routeHash({ connectionID: 'local', section: 'operations', itemID: operation.id, view: 'details' }))
      .toBe(`#/connections/local/operations/${operation.id}`);
    for (const hash of ['#/%', '#/connections/id/unknown', '#/connections/id/overview/item', '#/connections/id/operations/',
      '#/connections/id/operations/item/unknown', '#/connections/id/agents/item/graph', '#/connections/id/operations/item/graph/extra']) {
      expect(parseRoute(hash)).toEqual({});
    }
  });

  it('restores operation details and graph through browser history and restores the server session after reload', async () => {
    const fetch = mockAPI();
    visit({ connectionID: connection.id, section: 'overview' });
    const first = render(<App />);
    await screen.findByRole('button', { name: 'Sign in' });
    await signIn();

    fireEvent.click(screen.getByRole('button', { name: 'Operations' }));
    fireEvent.click(await screen.findByRole('button', { name: `View operation ${operation.id}` }));
    fireEvent.click(await screen.findByRole('button', { name: 'Execution graph' }));
    expect(await screen.findByText('No work recorded yet')).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/operations/${operation.id}/graph`);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel operation' }));
    expect(screen.getByRole('dialog')).toBeVisible();

    act(() => { window.history.back(); });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Details' })).toHaveAttribute('aria-pressed', 'true'));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    act(() => { window.history.back(); });
    expect(await screen.findByRole('heading', { name: 'Operations', level: 1 })).toBeVisible();
    act(() => { window.history.forward(); });
    await screen.findByRole('heading', { name: operation.id, level: 1 });
    act(() => { window.history.forward(); });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Execution graph' })).toHaveAttribute('aria-pressed', 'true'));

    first.unmount();
    fetch.mockClear();
    render(<App />);
    expect(await screen.findByRole('heading', { name: operation.id, level: 1 })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Sign in' })).not.toBeInTheDocument();
    expect(fetch.mock.calls.every(([, init]) => !new Headers(init?.headers).has('Authorization'))).toBe(true);
    expect(await screen.findByText('No work recorded yet')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Execution graph' })).toHaveAttribute('aria-pressed', 'true');
    expect(window.location.href).not.toContain('private-password');
    expect(window.location.hash).not.toContain(connection.url);
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).not.toContain('private-password');
    expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
  });

  it.each(['missions', 'controllers', 'agents', 'actions'] as const)('opens a saved %s detail location behind the sign-in gate', async section => {
    mockAPI();
    visit({ connectionID: connection.id, section, itemID: 'saved-item' });
    render(<App />);

    expect(await screen.findByRole('heading', { name: 'saved-item', level: 1 })).toBeVisible();
    expect(screen.getByRole('alert')).toHaveTextContent(`Sign in to view ${section}`);
    const hash = window.location.hash;
    fireEvent.click(screen.getByRole('link', { name: 'Skip to content' }));
    expect(screen.getByRole('main')).toHaveFocus();
    expect(window.location.hash).toBe(hash);
  });

  it('falls back to connections when a saved connection is missing, without calling a URL from the location', async () => {
    const fetch = mockAPI();
    visit({ connectionID: 'https://untrusted.example', section: 'operations', itemID: operation.id });
    render(<App />);

    expect(screen.getByRole('heading', { name: 'Connections' })).toBeVisible();
    expect(screen.getByText('This connection is not saved in this browser. Choose or add a connection.')).toBeVisible();
    await act(async () => {});
    expect(fetch.mock.calls.every(([url]) => String(url).startsWith('/ui/'))).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Open Local Commander' }));
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/overview`);
  });
});

import { upstreamURL } from './proxy';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Mission } from '../src/api/types';
import { MissionsPage } from '../src/pages/MissionsPage';

const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };
const credentials = { username: 'operator', url: connection.url };
const mission: Mission = {
  name: 'review-website-with-browser',
  objective: 'Review links and page quality.',
  required_actions: ['discover-pages', 'check-links'],
  starting_actions: ['discover-pages'],
  input_schema: {
    type: 'object', required: ['url'],
    properties: { url: { type: 'string', description: 'Website to review' }, max_pages: { type: 'integer', default: 3 } },
  },
  output_schema: { type: 'object', properties: { summary: { type: 'string' } } },
  rules: {
    'aggregate-results': { for_actions: ['discover-pages'] },
    'decide-next-actions': { for_actions: ['check-links', 'capture-page'], params: { decision_by: 'model' } },
  },
};
const otherMission: Mission = { ...mission, name: 'review-service', objective: 'Check service availability.' };

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

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search missions' }), { target: { value: 'PAGE QUALITY' } });
    expect(screen.getByText('1 of 2 missions')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'View mission review-service' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'View mission review-website-with-browser' }));

    expect(await screen.findByRole('heading', { name: 'Starting actions' })).toBeVisible();
    expect(screen.getByRole('heading', { name: mission.name, level: 1 })).toHaveFocus();
    expect(screen.getByText('Website to review')).toBeVisible();
    const rules = within(screen.getByRole('table', { name: 'Mission rules' }));
    expect(rules.getAllByRole('row')).toHaveLength(3);
    expect(rules.getByRole('row', { name: 'Aggregate results discover-pages All direct children' })).toBeVisible();
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
      name: 'manual-review', objective: 'A manual review.', starting_actions: null, required_actions: null,
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
});

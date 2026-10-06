import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Action } from '../src/api/types';
import { ActionsPage } from '../src/pages/ActionsPage';

const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };
const credentials = { username: 'operator', password: 'test-password' };
const action: Action = {
  name: 'capture-page', description: 'Capture a screenshot of a page.', status: 'unavailable',
  status_reason: 'Chromium and configuration are missing.', required_permissions: ['network-access'],
  missing_requirements: [{ kind: 'executable', name: 'chromium' }, { kind: 'configuration', name: 'BROWSER_TOKEN', secret: true }],
  input_schema: { type: 'object', required: ['url'], properties: { url: { type: 'string', description: 'Page to capture' } } },
  output_schema: { type: 'object', properties: { width: { type: 'integer' } } },
};
const otherAction: Action = { ...action, name: 'check-links', description: 'Check page links.', status: 'unknown', status_reason: undefined, missing_requirements: [] };

function mockAPI(respond?: (url: string, init: RequestInit) => Response | Promise<Response>) {
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    if (respond) return respond(String(input), init);
    return String(input).endsWith('/api/v1/actions') ? Response.json({ items: [action, otherAction] })
      : Response.json(String(input).endsWith(otherAction.name) ? otherAction : action);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

describe('actions browser', () => {
  it('searches descriptions, displays readiness and requirements, and preserves search on return', async () => {
    const fetch = mockAPI();
    render(<ActionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('button', { name: 'View action capture-page' })).toBeVisible();
    expect(screen.getByText('2 actions')).toBeVisible();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search actions' }), { target: { value: 'SCREENSHOT' } });
    expect(screen.getByText('1 of 2 actions')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'View action check-links' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'View action capture-page' }));

    expect(await screen.findByRole('heading', { name: 'Required permissions' })).toBeVisible();
    expect(screen.getByRole('heading', { name: 'capture-page', level: 1 })).toHaveFocus();
    expect(screen.getByText('Unavailable')).toBeVisible();
    expect(screen.getByText('Chromium and configuration are missing.')).toBeVisible();
    expect(screen.getByText('network-access')).toBeVisible();
    expect(screen.getByText('chromium')).toBeVisible();
    expect(screen.getByText('BROWSER_TOKEN')).toBeVisible();
    expect(screen.getByText(/Secret configuration/)).toBeVisible();
    expect(screen.getByText('Page to capture')).toBeVisible();
    expect(screen.getByText('Readiness and missing requirements are reported per agent. View Agents to see which can run this action.')).toBeVisible();
    expect(fetch.mock.calls.every(([, init]) => init?.method === 'GET')).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'All actions' }));

    expect(await screen.findByRole('searchbox')).toHaveValue('SCREENSHOT');
    expect(screen.getByRole('heading', { name: 'Actions', level: 1 })).toHaveFocus();
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'absent' } });
    expect(screen.getByRole('heading', { name: 'No matching actions' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
    expect(screen.getByRole('button', { name: 'View action check-links' })).toBeVisible();
    expect(screen.queryByText('Unknown')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'View action check-links' }));

    expect(await screen.findByRole('heading', { name: 'Availability' })).toBeVisible();
    expect(screen.queryByText('Unknown')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Missing requirements' })).not.toBeInTheDocument();
  });

  it('handles empty catalogs, nullable details and new status values without claiming readiness', async () => {
    const minimal: Action = { name: 'custom-action', description: 'A custom action.', status: 'maintenance', required_permissions: null, missing_requirements: null, input_schema: null, output_schema: null };
    let items: Action[] = [];
    mockAPI(url => url.endsWith('/api/v1/actions') ? Response.json({ items }) : Response.json(minimal));
    render(<ActionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('heading', { name: 'No actions yet' })).toBeVisible();
    expect(screen.queryByRole('searchbox')).not.toBeInTheDocument();

    items = [minimal];
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    fireEvent.click(await screen.findByRole('button', { name: 'View action custom-action' }));

    expect(await screen.findByText('No missing requirements reported.')).toBeVisible();
    expect(screen.getByText('maintenance')).toHaveAttribute('data-tone', 'muted');
    expect(screen.getByText('None specified')).toBeVisible();
    expect(screen.getAllByText('No schema specified.')).toHaveLength(2);
  });

  it('requires authentication and hides loaded data when credentials are forgotten', async () => {
    let reject = false;
    const fetch = mockAPI(() => reject ? new Response(null, { status: 401 }) : Response.json({ items: [action] }));
    const onSignIn = vi.fn();
    const view = render(<ActionsPage connection={connection} onSignIn={onSignIn} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Sign in to view actions on this connection.');
    expect(fetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(onSignIn).toHaveBeenCalledOnce();

    view.rerender(<ActionsPage connection={connection} credentials={credentials} onSignIn={onSignIn} />);
    expect(await screen.findByRole('button', { name: 'View action capture-page' })).toBeVisible();
    view.rerender(<ActionsPage connection={connection} onSignIn={onSignIn} />);
    expect(screen.queryByRole('button', { name: 'View action capture-page' })).not.toBeInTheDocument();

    reject = true;
    view.rerender(<ActionsPage connection={connection} credentials={credentials} onSignIn={onSignIn} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Sign in to view actions on this connection.');
    expect(screen.queryByRole('heading', { name: 'No actions yet' })).not.toBeInTheDocument();
  });

  it('retries catalog errors and handles a removed action without showing server internals', async () => {
    let failed = true;
    mockAPI(url => url.endsWith('/api/v1/actions')
      ? failed ? Response.json({ error: 'database-secret-value' }, { status: 503 }) : Response.json({ items: [action] })
      : new Response(null, { status: 404 }));
    render(<ActionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load actions. Check the connection and try again.');
    expect(screen.queryByText('database-secret-value')).not.toBeInTheDocument();

    failed = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    fireEvent.click(await screen.findByRole('button', { name: 'View action capture-page' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('This action is no longer available.');
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'All actions' }));
    expect(await screen.findByRole('button', { name: 'View action capture-page' })).toBeVisible();
  });

  it('cancels a detail request on return and ignores its late response', async () => {
    let finish!: (response: Response) => void;
    const response = new Promise<Response>(resolve => { finish = resolve; });
    let signal: AbortSignal | undefined;
    mockAPI((url, init) => {
      if (url.endsWith('/api/v1/actions')) return Response.json({ items: [action] });
      signal = init.signal ?? undefined;
      return response;
    });
    render(<ActionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'View action capture-page' }));
    expect(screen.getByText('Loading action…')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'All actions' }));
    expect(signal?.aborted).toBe(true);

    await act(async () => finish(Response.json(action)));

    expect(await screen.findByRole('button', { name: 'View action capture-page' })).toBeVisible();
    expect(screen.queryByRole('heading', { name: 'Required permissions' })).not.toBeInTheDocument();
  });
});

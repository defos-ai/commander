import { upstreamURL } from './proxy';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Action, Agent } from '../src/api/types';
import { AgentsPage } from '../src/pages/AgentsPage';

const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };
const credentials = { username: 'operator', url: connection.url };
const action: Action = {
  name: 'check-links', description: 'Check website links.', status: 'ready', required_permissions: ['network'],
  input_schema: {}, output_schema: {},
};
const browserAction: Action = {
  ...action, name: 'capture-page', description: 'Capture a rendered page.', status: 'unavailable',
  status_reason: 'Chromium and browser configuration are missing.',
  missing_requirements: [
    { kind: 'executable', name: 'chromium' },
    { kind: 'configuration', name: 'BROWSER_TOKEN', secret: true },
  ],
};
const agent: Agent = {
  id: 'browser-agent-a1234567', type: 'browser-agent', status: 'working',
  work: { action_name: 'check-links', operation_id: 'operation-123', task_id: 'task-456', step_id: 'step-789', command_id: 'command-012' },
  server: { hostname: 'worker-west-1', ip_address: '10.0.0.12' },
  responsibilities: ['Website quality'], actions: [action, browserAction], granted_permissions: ['network'],
  discovery_scope: null, config: { secret: 'DO-NOT-DISPLAY-CONFIG' },
  observed_at: '2026-10-05T10:01:00Z', last_seen: '2026-10-05T10:01:00Z', last_heartbeat: '2026-10-05T10:00:45Z',
  lease_expires_at: '2026-10-05T10:01:30Z', stale: false,
};
const staleAgent: Agent = { ...agent, id: 'browser-agent-b1234567', status: 'unavailable', stale: true,
  server: { ...agent.server, hostname: 'worker-east-1' } };

function mockAPI(respond?: (url: string) => Response | Promise<Response>) {
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    if (respond) return respond(upstreamURL(input));
    return upstreamURL(input).endsWith('/api/v1/agents') ? Response.json({ items: [agent, staleAgent] }) : Response.json(agent);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

describe('agents browser', () => {
  it('searches snapshots and distinguishes current work from stale reported work', async () => {
    mockAPI();
    render(<AgentsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    const working = await screen.findByRole('button', { name: `View agent ${agent.id}` });
    const stale = screen.getByRole('button', { name: `View agent ${staleAgent.id}` });
    expect(within(working).getByText('Working')).toBeVisible();
    expect(within(working).getByText('Current work: check-links')).toBeVisible();
    expect(within(working).getByText('1 / 2 actions reported ready')).toBeVisible();
    expect(within(stale).getByText('Stale')).toBeVisible();
    expect(within(stale).getByText('Unavailable')).toBeVisible();
    expect(within(stale).getByText('Last reported work: check-links')).toBeVisible();
    expect(within(stale).queryByText('Current work: check-links')).not.toBeInTheDocument();

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search agents' }), { target: { value: 'WORKER-WEST' } });
    expect(screen.getByText('1 of 2 agents')).toBeVisible();
    expect(screen.queryByRole('button', { name: `View agent ${staleAgent.id}` })).not.toBeInTheDocument();
    fireEvent.click(working);
    expect(await screen.findByRole('heading', { name: 'Agent snapshot' })).toBeVisible();
    expect(screen.getByRole('heading', { name: agent.id, level: 1 })).toHaveFocus();
    fireEvent.click(screen.getByRole('button', { name: 'All agents' }));
    expect(await screen.findByRole('searchbox', { name: 'Search agents' })).toHaveValue('WORKER-WEST');
    expect(screen.getByRole('heading', { name: 'Agents', level: 1 })).toHaveFocus();

    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'absent-host' } });
    expect(screen.getByRole('heading', { name: 'No matching agents' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
    expect(screen.getByText('2 agents')).toBeVisible();
  });

  it('shows work identifiers and per-agent dependencies without exposing configuration secrets or commands', async () => {
    mockAPI();
    render(<AgentsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: `View agent ${agent.id}` }));
    expect(await screen.findByRole('heading', { name: 'Current work' })).toBeVisible();
    for (const value of ['operation-123', 'task-456', 'step-789', 'command-012', '10.0.0.12', 'worker-west-1', 'Website quality']) {
      expect(screen.getByText(value)).toBeVisible();
    }
    expect(screen.getByRole('heading', { name: 'Granted permissions' })).toBeVisible();
    expect(screen.getByText('Last heartbeat')).toBeVisible();
    expect(screen.getByText('Lease expires at')).toBeVisible();
    const summary = screen.getByText('capture-page').closest('summary');
    expect(summary).not.toBeNull();
    fireEvent.click(summary!);
    expect(screen.getByText('Chromium and browser configuration are missing.')).toBeVisible();
    expect(screen.getByText('chromium')).toBeVisible();
    expect(screen.getByText('BROWSER_TOKEN')).toBeVisible();
    expect(screen.getByText('BROWSER_TOKEN').closest('li')).toHaveTextContent('Configuration: BROWSER_TOKEN · Secret');
    expect(screen.queryByText('DO-NOT-DISPLAY-CONFIG')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /stop agent|run action|discover actions/i })).not.toBeInTheDocument();
  });

  it('keeps stale detail work explicitly historical and handles absent fields without inventing readiness', async () => {
    const snapshot: Agent = { ...staleAgent, responsibilities: null, granted_permissions: null, actions: null,
      observed_at: undefined, lease_expires_at: undefined, last_heartbeat: '0001-01-01T00:00:00Z' };
    mockAPI(url => url.endsWith('/api/v1/agents') ? Response.json({ items: [snapshot] }) : Response.json(snapshot));
    render(<AgentsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: `View agent ${snapshot.id}` }));

    expect(await screen.findByRole('heading', { name: 'Last reported work' })).toBeVisible();
    expect(screen.getByText('This snapshot is stale. Reported work and action readiness may have changed.')).toBeVisible();
    expect(screen.queryByRole('heading', { name: 'Current work' })).not.toBeInTheDocument();
    expect(screen.getByText('No actions reported.')).toBeVisible();
    expect(screen.getByText('Last heartbeat').parentElement).toHaveTextContent('Not reported');
    expect(screen.queryByText(/0001/)).not.toBeInTheDocument();
  });

  it('refreshes work automatically and clears old snapshots when polling fails', async () => {
    vi.useFakeTimers();
    try {
      let response = Response.json({ items: [agent] });
      const fetch = mockAPI(() => response);
      await act(async () => { render(<AgentsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />); });
      expect(screen.getByText('Current work: check-links')).toBeVisible();

      response = Response.json({ items: [{ ...agent, status: 'ready', work: null }] });
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });

      expect(fetch).toHaveBeenCalledTimes(2);
      expect(screen.queryByText('Current work: check-links')).not.toBeInTheDocument();
      expect(screen.getByText('No active work reported')).toBeVisible();
      expect(screen.getByText('Ready')).toBeVisible();

      response = Response.json({ error: 'database credentials must not be shown' }, { status: 503 });
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });

      expect(screen.getByRole('alert')).toHaveTextContent('Could not load agents. Check the connection and try again.');
      expect(screen.queryByRole('button', { name: `View agent ${agent.id}` })).not.toBeInTheDocument();
      expect(screen.queryByText('Ready')).not.toBeInTheDocument();
      expect(screen.queryByText('database credentials must not be shown')).not.toBeInTheDocument();

      response = Response.json({ items: [agent] });
      fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
      await act(async () => {});
      expect(screen.getByText('Current work: check-links')).toBeVisible();
    } finally { vi.useRealTimers(); }
  });

  it('requires sign-in and stops automatic requests after credentials are rejected', async () => {
    vi.useFakeTimers();
    try {
      const fetch = mockAPI(() => new Response(null, { status: 401 }));
      const onSignIn = vi.fn();
      const view = render(<AgentsPage connection={connection} onSignIn={onSignIn} />);
      expect(fetch).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
      expect(onSignIn).toHaveBeenCalledOnce();
      await act(async () => { view.rerender(<AgentsPage connection={connection} credentials={credentials} onSignIn={onSignIn} />); });
      expect(screen.getByRole('alert')).toHaveTextContent('Sign in to view agents on this connection.');
      await act(async () => { await vi.advanceTimersByTimeAsync(90_000); });
      expect(fetch).toHaveBeenCalledOnce();

      fetch.mockResolvedValue(Response.json({ items: [agent] }));
      await act(async () => { view.rerender(<AgentsPage connection={connection} credentials={{ ...credentials, username: 'second-operator' }} onSignIn={onSignIn} />); });

      expect(screen.getByRole('button', { name: `View agent ${agent.id}` })).toBeVisible();
      expect(fetch).toHaveBeenCalledTimes(2);
    } finally { vi.useRealTimers(); }
  });

  it('returns to an empty list when an agent disappears before its snapshot opens', async () => {
    let items = [agent];
    mockAPI(url => url.endsWith('/api/v1/agents') ? Response.json({ items }) : new Response(null, { status: 404 }));
    render(<AgentsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: `View agent ${agent.id}` }));
    expect(await screen.findByRole('alert')).toHaveTextContent('This agent is no longer available.');
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
    items = [];
    fireEvent.click(screen.getByRole('button', { name: 'All agents' }));

    expect(await screen.findByRole('heading', { name: 'No agents yet' })).toBeVisible();
    expect(screen.queryByRole('searchbox')).not.toBeInTheDocument();
  });
});

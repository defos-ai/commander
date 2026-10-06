import { upstreamURL } from './proxy';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Operation, OperationTask } from '../src/api/operation-types';
import { OperationsPage } from '../src/pages/OperationsPage';

const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };
const credentials = { username: 'operator', url: connection.url };
const now = '2026-10-05T10:00:00Z';
const task: OperationTask = {
  id: 'task-discovery', operation_id: 'operation-review', agent_id: 'web-quality-agent-1234abcd',
  action_name: 'discover-pages', status: 'succeeded', input: { url: 'https://defos.ai' },
  started_at: now, finished_at: now, result: { action_name: 'discover-pages', output: { pages: ['https://defos.ai'] } },
};
const operation: Operation = {
  id: 'operation-review', mission: { name: 'review-website', objective: 'Review website quality.',
    required_actions: ['discover-pages'], starting_actions: ['discover-pages'], input_schema: {}, output_schema: {} },
  input: { url: 'https://defos.ai' }, status: 'planning', tasks: [task], questions: [], events: [], created_at: now, updated_at: now,
};

function mockAPI(respond?: (url: string, options: RequestInit) => Response | Promise<Response>) {
  const fetch = vi.fn(async (input: RequestInfo | URL, options: RequestInit = {}) => respond ? respond(upstreamURL(input), options)
    : Response.json(upstreamURL(input).endsWith('/operations') ? { items: [operation] } : operation));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

describe('operations browser', () => {
  it('searches and filters operations while keeping task progress separate from mission completion', async () => {
    const other: Operation = { ...operation, id: 'operation-completed', status: 'completed', mission: { ...operation.mission, name: 'other-review' } };
    mockAPI(url => Response.json(url.endsWith('/operations') ? { items: [operation, other] } : operation));
    render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);

    const row = await screen.findByRole('button', { name: `View operation ${operation.id}` });
    expect(within(row).getByText('Planning')).toBeVisible();
    expect(within(row).getByText('1/1 tasks finished')).toBeVisible();
    expect(within(row).queryByText('Completed')).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'REVIEW-WEBSITE' } });
    expect(screen.getByText('1 of 2 operations')).toBeVisible();
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'completed' } });
    expect(screen.getByRole('heading', { name: 'No matching operations' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(screen.getByText('2 operations')).toBeVisible();
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: operation.id } });
    fireEvent.click(screen.getByRole('button', { name: `View operation ${operation.id}` }));

    expect(await screen.findByText('Review website quality.')).toBeVisible();
    expect(screen.getByRole('heading', { level: 1, name: operation.id })).toHaveFocus();
    fireEvent.click(screen.getByRole('button', { name: 'All operations' }));
    expect(await screen.findByRole('searchbox')).toHaveValue(operation.id);
    expect(screen.getByRole('heading', { level: 1, name: 'Operations' })).toHaveFocus();
  });

  it('shows saved task results, questions, blocking requirements and artifact file metadata without sending commands', async () => {
    const waiting: Operation = { ...operation, status: 'waiting', preflight: { ready: false, version: 1, actions: [
      { action_name: 'capture-page', unavailable_agents: [{ agent_id: task.agent_id, reason: 'Browser dependency missing',
        missing_requirements: [{ kind: 'executable', name: 'chromium' }] }] },
    ] }, tasks: [{ ...task, error: 'Partial page response', continuation: { status: 'waiting', decision_by: 'user', version: 2,
      proposed_actions: [{ action_name: 'capture-page', reason: 'Check rendering' }], created_task_ids: [], question_id: 'question-browser' },
      result: { action_name: task.action_name, output: { page_count: 1 }, artifacts: [{ id: 'artifact-screenshot', name: 'Desktop capture',
        agent_id: task.agent_id, action_name: task.action_name, created_at: now,
        files: [{ id: 'file-desktop', name: 'desktop.png', media_type: 'image/png', size: 1200, sha256: 'a'.repeat(64) }] }] } }],
      questions: [{ id: 'question-browser', task_id: task.id, status: 'pending', text: 'How should this review continue?',
        options: ['Configure the browser', 'Review without screenshots'], asked_at: now, expires_at: '2099-01-01T00:00:00Z' }],
    };
    const fetch = mockAPI(url => Response.json(url.endsWith('/operations') ? { items: [waiting] } : waiting));
    render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: `View operation ${operation.id}` }));

    expect(await screen.findByText('How should this review continue?')).toBeVisible();
    expect(screen.getByText('Configure the browser')).toBeVisible();
    fireEvent.click(screen.getByText('capture-page', { selector: 'summary *' }).closest('summary')!);
    expect(screen.getByText('Browser dependency missing')).toBeVisible();
    expect(screen.getByText('chromium')).toBeVisible();
    fireEvent.click(screen.getByText(task.action_name, { selector: 'summary *' }).closest('summary')!);
    expect(screen.getByText('Partial page response')).toBeVisible();
    expect(screen.getByText('Check rendering')).toBeVisible();
    fireEvent.click(screen.getByText('Desktop capture', { selector: 'summary *' }).closest('summary')!);
    expect(screen.getByText('desktop.png')).toBeVisible();
    expect(screen.getByText('image/png')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Answer question' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Cancel operation' })).toBeVisible();
    expect(screen.queryByRole('button', { name: /run action/i })).not.toBeInTheDocument();
    expect(fetch.mock.calls.every(([, options]) => options?.method === 'GET')).toBe(true);
  });

  it('polls progress, clears unavailable snapshots, and recovers with refresh', async () => {
    vi.useFakeTimers();
    try {
      let snapshot = operation;
      let unavailable = false;
      const fetch = mockAPI(url => unavailable ? new Response(null, { status: 503 })
        : Response.json(url.endsWith('/operations') ? { items: [snapshot] } : snapshot));
      await act(async () => { render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />); });
      fireEvent.click(screen.getByRole('button', { name: `View operation ${operation.id}` }));
      await act(async () => {});
      snapshot = { ...operation, status: 'completed', output: { findings: ['Broken contact link'] }, report_id: 'report-123' };

      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });

      expect(screen.getByText('Completed', { selector: '.catalog-status' })).toBeVisible();
      expect(screen.getByText('report-123')).toBeVisible();
      unavailable = true;
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(screen.getByRole('alert')).toHaveTextContent('Could not load operations');
      expect(screen.queryByText('report-123')).not.toBeInTheDocument();
      unavailable = false;
      fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
      await act(async () => {});
      expect(screen.getByText('report-123')).toBeVisible();
      expect(fetch.mock.calls.every(([, options]) => options?.method === 'GET')).toBe(true);
    } finally { vi.useRealTimers(); }
  });

  it('stops polling a deleted operation and supports returning to an empty list', async () => {
    vi.useFakeTimers();
    try {
      let removed = false;
      const fetch = mockAPI(url => url.endsWith('/operations') ? Response.json({ items: removed ? [] : [operation] })
        : new Response(null, { status: 404 }));
      await act(async () => { render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />); });
      fireEvent.click(screen.getByRole('button', { name: `View operation ${operation.id}` }));
      await act(async () => {});
      expect(screen.getByRole('alert')).toHaveTextContent('no longer available');
      const count = fetch.mock.calls.length;
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
      expect(fetch).toHaveBeenCalledTimes(count);
      removed = true;
      fireEvent.click(screen.getByRole('button', { name: 'All operations' }));
      await act(async () => {});
      expect(screen.getByRole('heading', { name: 'No operations yet' })).toBeVisible();
    } finally { vi.useRealTimers(); }
  });

  it('loads the execution graph only when opened and stops its polling when returning to details', async () => {
    vi.useFakeTimers();
    try {
      const graph = { operation_id: operation.id, status: operation.status, updated_at: now,
        nodes: [{ id: `task:${task.id}`, kind: 'task', task: { id: task.id, action_name: task.action_name, status: task.status, agent_id: task.agent_id } }], edges: [] };
      const fetch = mockAPI(url => Response.json(url.endsWith('/graph') ? graph
        : url.endsWith('/operations') ? { items: [operation] } : operation));
      await act(async () => { render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />); });
      fireEvent.click(screen.getByRole('button', { name: `View operation ${operation.id}` }));
      await act(async () => {});
      const graphCalls = () => fetch.mock.calls.filter(([url]) => upstreamURL(url).endsWith('/graph')).length;
      expect(graphCalls()).toBe(0);

      fireEvent.click(screen.getByRole('button', { name: 'Execution graph' }));
      await act(async () => {});

      expect(screen.getByRole('button', { name: `Inspect Task ${task.action_name} · task:${task.id}` })).toBeVisible();
      expect(graphCalls()).toBe(1);
      expect(screen.getAllByRole('button', { name: 'Refresh' })).toHaveLength(1);
      expect(screen.getByRole('button', { name: 'Refresh' })).toBeVisible();
      const beforeInspect = fetch.mock.calls.length;
      fireEvent.click(screen.getByRole('button', { name: `Inspect Task ${task.action_name} · task:${task.id}` }));
      const inspector = within(screen.getByRole('region', { name: 'Node details' }));
      for (const [label, value] of [['Input', task.input], ['Output', task.result!.output]] as const) {
        const summary = inspector.getByText(label, { selector: 'summary' });
        fireEvent.click(summary);
        expect(summary.closest('details')?.querySelector('pre')?.textContent).toBe(JSON.stringify(value, null, 2));
      }
      expect(fetch).toHaveBeenCalledTimes(beforeInspect);
      fireEvent.click(screen.getByRole('button', { name: 'Close dialog' }));
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(graphCalls()).toBe(2);
      fireEvent.click(screen.getByRole('button', { name: 'Details' }));
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(graphCalls()).toBe(2);
      expect(screen.getByText('Review website quality.')).toBeVisible();
      expect(fetch.mock.calls.every(([, options]) => options?.method === 'GET')).toBe(true);
    } finally { vi.useRealTimers(); }
  });

  it('does not show late results from a previous connection or keep data after credentials are forgotten', async () => {
    let finish!: (response: Response) => void;
    const delayed = new Promise<Response>(resolve => { finish = resolve; });
    let oldSignal: AbortSignal | null | undefined;
    const fetch = mockAPI((url, options) => {
      if (url.startsWith(connection.url)) { oldSignal = options.signal; return delayed; }
      return Response.json({ items: [] });
    });
    const view = render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    const other = { ...connection, id: 'other', url: 'https://other.example.test' };

    view.rerender(<OperationsPage connection={other} credentials={{ ...credentials, url: other.url }} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('heading', { name: 'No operations yet' })).toBeVisible();
    expect(oldSignal?.aborted).toBe(true);
    await act(async () => finish(Response.json({ items: [operation] })));
    expect(screen.queryByRole('button', { name: `View operation ${operation.id}` })).not.toBeInTheDocument();
    const calls = fetch.mock.calls.length;
    view.rerender(<OperationsPage connection={other} onSignIn={vi.fn()} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Sign in to view operations');
    expect(fetch).toHaveBeenCalledTimes(calls);
  });
});

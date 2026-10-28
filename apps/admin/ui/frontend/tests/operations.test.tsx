import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Operation } from '../src/api/operation-types';
import { OperationsPage } from '../src/pages/OperationsPage';
import { upstreamURL } from './fakes/proxy';
import { connection, credentials, mockAPI, now, operation, secretWait, task } from './fixtures/operations';

describe('operations browser', () => {
  it('shows affinity before dispatch, preserves its owner, and limits queue hints to grouped active tasks', async () => {
    const actions = ['checkout-template', 'build-agent'];
    const owner = 'builder-agent-1234abcd';
    const queued = { ...task, agent_id: '', status: 'queued', finished_at: undefined, result: undefined };
    let snapshot: Operation = {
      ...operation, mission: { ...operation.mission, rules: { 'same-agent': { for_actions: actions } } },
      affinity: { for_actions: actions },
      progress: { task_counts: { queued: 2 }, total_tasks: 2, finished_tasks: 0, pending_decisions: 0, pending_questions: 0, settled: false },
      tasks: [{ ...queued, id: 'task-build', action_name: 'build-agent' }, { ...queued, id: 'task-other', action_name: 'other-action' }],
    };
    mockAPI(() => Response.json(snapshot));
    render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} selectedID={operation.id}
      onSelect={vi.fn()} />);

    expect(await screen.findByRole('heading', { name: 'Same agent' })).toBeVisible();
    expect(screen.getByText('The first dispatched action selects the agent.')).toBeVisible();
    expect(screen.getByText('These actions stay on one agent for this operation. No automatic fallback.')).toBeVisible();
    for (const action of ['build-agent', 'other-action']) {
      fireEvent.click(screen.getByText(action, { selector: 'summary *' }).closest('summary')!);
    }
    expect(screen.queryByText(`Queued for ${owner}.`)).not.toBeInTheDocument();

    snapshot = { ...snapshot, affinity: { for_actions: actions, agent_id: owner }, preflight: { ready: false, version: 1, actions: [
      { action_name: 'build-agent', unavailable_agents: [{ agent_id: owner, reason: 'Agent required by same-agent is not registered' }] },
    ] } };
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));

    expect(await screen.findByText(`Queued for ${owner}.`)).toBeVisible();
    expect(screen.getAllByText(`Queued for ${owner}.`)).toHaveLength(1);
    expect(screen.queryByText('The first dispatched action selects the agent.')).not.toBeInTheDocument();
    const preflight = screen.getByRole('heading', { name: 'Agent readiness' }).closest('section')!;
    fireEvent.click(within(preflight).getByText('build-agent').closest('summary')!);
    expect(screen.getByText('Agent required by same-agent is not registered')).toBeVisible();

    snapshot = { ...snapshot, status: 'completed' };
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(await screen.findByText('Completed', { selector: '.catalog-status' })).toBeVisible();
    expect(screen.queryByText(`Queued for ${owner}.`)).not.toBeInTheDocument();
    const affinity = screen.getByRole('heading', { name: 'Same agent' }).closest('section')!;
    expect(within(affinity).getByText(owner)).toBeVisible();

    snapshot = { ...snapshot, status: 'waiting', affinity: { for_actions: actions, error: 'Conflicting assignments' } };
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Conflicting assignments');
    expect(screen.queryByText(`Queued for ${owner}.`)).not.toBeInTheDocument();
    expect(screen.queryByText('The first dispatched action selects the agent.')).not.toBeInTheDocument();

    snapshot = { ...operation };
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await act(async () => {});
    expect(screen.queryByRole('heading', { name: 'Same agent' })).not.toBeInTheDocument();
  });

  it('reuses the secret notice for running work and clears it without a saved timestamp change', async () => {
    let snapshot: Operation = { ...operation, status: 'running',
      progress: { task_counts: { running: 1 }, total_tasks: 1, finished_tasks: 0, pending_decisions: 0, pending_questions: 0, settled: false },
      preflight: { ready: true, version: 1, actions: [] }, tasks: [
      { ...task, status: 'running', finished_at: undefined, result: undefined, agent_type: 'web-agent',
        deadline_at: new Date(Date.now() + 60_000).toISOString(), missing_requirements: [{ kind: 'secret', name: 'TOKEN' }] },
    ] };
    mockAPI(() => Response.json(snapshot));
    const manage = vi.fn();
    render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} selectedID={operation.id}
      onSelect={vi.fn()} onManageSecrets={manage} />);

    expect(await screen.findByText('Secrets needed')).toBeVisible();
    expect(screen.getAllByText('web-agent / TOKEN')).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Manage secrets' }));
    expect(manage).toHaveBeenCalledTimes(1);

    snapshot = { ...snapshot, tasks: [{ ...snapshot.tasks![0], missing_requirements: [] }] };
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await act(async () => {});
    expect(screen.queryByText('Secrets needed')).not.toBeInTheDocument();
  });

  it('shows managed secrets in details and graph, then clears the notice when readiness recovers', async () => {
    let snapshot = secretWait;
    const fetch = mockAPI(url => url.endsWith('/graph')
      ? Response.json({ operation_id: operation.id, status: snapshot.status, updated_at: now, nodes: [], edges: [] })
      : Response.json(snapshot));
    const manage = vi.fn();
    render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} selectedID={operation.id}
      onSelect={vi.fn()} onManageSecrets={manage} />);

    expect(await screen.findByText('Secrets needed')).toBeVisible();
    expect(screen.getAllByText('builder / TOKEN')).toHaveLength(1);
    fireEvent.click(screen.getByText('build', { selector: 'summary *' }).closest('summary')!);
    expect(screen.getAllByText('TOKEN')[0].closest('li')).toHaveTextContent('Secret: TOKEN');
    fireEvent.click(screen.getByRole('button', { name: 'Manage secrets' }));
    expect(manage).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Execution graph' }));
    expect(await screen.findByText('No work recorded yet')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Manage secrets' })).toBeVisible();

    snapshot = { ...secretWait, status: 'planning', preflight: { ...secretWait.preflight!, ready: true, version: 2 } };
    fireEvent.click(screen.getByRole('button', { name: 'Details' }));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(await screen.findByText('Planning', { selector: '.catalog-status' })).toBeVisible();
    expect(screen.queryByText('Secrets needed')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Manage secrets' })).not.toBeInTheDocument();
    expect(fetch.mock.calls.every(([url, init]) => !upstreamURL(url).includes('/secrets/requests') && init?.method === 'GET')).toBe(true);
  });

  it.each([
    ...['completed', 'cancelled', 'failed'].map(status => ({ ...secretWait, status })),
    { ...secretWait, preflight: { ...secretWait.preflight!, ready: true } },
    { ...secretWait, preflight: { ...secretWait.preflight!, actions: [{ ...secretWait.preflight!.actions![0], ready_agent_ids: ['ready-agent'] }] } },
    { ...secretWait, preflight: { ...secretWait.preflight!, actions: [{ action_name: 'build', unavailable_agents: [
      { agent_id: 'builder-agent-1234abcd', reason: 'Local configuration missing',
        missing_requirements: [{ kind: 'configuration', name: 'LOCAL_TOKEN', sensitive: true }] },
    ] }] } },
  ])('does not infer a secret wait from unrelated or historical requirements: %j', async snapshot => {
    mockAPI(() => Response.json(snapshot));
    render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} selectedID={operation.id}
      onSelect={vi.fn()} onManageSecrets={vi.fn()} />);

    expect(await screen.findByText('Review website quality.')).toBeVisible();
    expect(screen.queryByText('Secrets needed')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Manage secrets' })).not.toBeInTheDocument();
  });

  it('searches and filters operations while keeping task progress separate from mission completion', async () => {
    const other: Operation = { ...operation, id: 'operation-completed', status: 'completed', progress: { ...operation.progress, settled: true },
      mission: { ...operation.mission, name: 'other-review' } };
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
      { action_name: 'capture-page', unavailable_agents: [{ agent_id: 'browser-agent-1234abcd', reason: 'Browser dependency missing',
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

  it('refreshes cancelled interrupted work until a late outcome, clears unavailable snapshots, and recovers', async () => {
    vi.useFakeTimers();
    try {
      let snapshot: Operation = { ...operation, status: 'cancelled', tasks: [{ ...task, status: 'interrupted', result: undefined }],
        progress: { task_counts: { interrupted: 1 }, total_tasks: 1, finished_tasks: 0, pending_decisions: 0, pending_questions: 0, settled: false } };
      let unavailable = false;
      const fetch = mockAPI(url => unavailable ? new Response(null, { status: 503 })
        : Response.json(url.endsWith('/operations') ? { items: [snapshot] } : snapshot));
      await act(async () => { render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />); });
      const row = screen.getByRole('button', { name: `View operation ${operation.id}` });
      expect(within(row).getByText('Cancelled')).toBeVisible();
      expect(within(row).getByText('0/1 tasks finished')).toBeVisible();
      fireEvent.click(row);
      await act(async () => {});
      expect(screen.getByText('0/1 tasks finished')).toBeVisible();
      expect(screen.queryByRole('button', { name: 'Cancel operation' })).not.toBeInTheDocument();
      snapshot = { ...snapshot, tasks: [{ ...snapshot.tasks![0], status: 'failed' }],
        progress: { ...snapshot.progress, task_counts: { failed: 1 }, finished_tasks: 1, settled: true } };

      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });

      expect(screen.getByText('Cancelled', { selector: '.catalog-status' })).toBeVisible();
      expect(screen.getByText('1/1 tasks finished')).toBeVisible();
      expect(screen.queryByRole('button', { name: 'Cancel operation' })).not.toBeInTheDocument();
      unavailable = true;
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(screen.getByRole('alert')).toHaveTextContent('Could not load operations');
      expect(screen.queryByText('1/1 tasks finished')).not.toBeInTheDocument();
      unavailable = false;
      fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
      await act(async () => {});
      expect(screen.getByText('1/1 tasks finished')).toBeVisible();
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

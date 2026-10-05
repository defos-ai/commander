import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { OperationGraph } from '../src/api/graph-types';
import { ExecutionGraph } from '../src/components/ExecutionGraph';
import { OperationGraphPanel } from '../src/components/OperationGraphPanel';

const url = 'https://commander.example.test';
const credentials = { username: 'operator', password: 'secret' };
const graph: OperationGraph = { operation_id: 'operation-1', status: 'running', updated_at: '2026-10-05T10:00:00Z', nodes: [
  { id: 'task:root', kind: 'task', task: { id: 'root', action_name: 'discover-pages', status: 'succeeded', agent_id: 'agent-one' } },
  { id: 'task:child', kind: 'task', task: { id: 'child', parent_task_id: 'root', action_name: 'capture-page', status: 'running', agent_id: 'agent-two' } },
  { id: 'artifact:page', kind: 'artifact', artifact: { id: 'page', name: 'Homepage evidence', agent_id: 'agent-two', action_name: 'capture-page', created_at: '2026-10-05T10:00:00Z' } },
  { id: 'file:page:desktop', kind: 'file', file: { id: 'desktop', artifact_id: 'page', name: 'desktop.png', media_type: 'image/png', size: 1024, sha256: 'a'.repeat(64) } },
], edges: [
  { source: 'task:root', target: 'task:child', kind: 'parent_child' },
  { source: 'task:child', target: 'artifact:page', kind: 'produced' },
  { source: 'artifact:page', target: 'file:page:desktop', kind: 'contains' },
] };

describe('execution graph', () => {
  it('lets operators inspect task, artifact and file relationships and zoom the canvas', () => {
    render(<ExecutionGraph graph={graph} />);
    fireEvent.click(screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' }));
    const inspector = screen.getByRole('region', { name: 'Node details' });
    expect(within(inspector).getByText('agent-two')).toBeVisible();
    expect(within(inspector).getByText('Running')).toBeVisible();
    expect(within(inspector).getByRole('button', { name: 'Parent task discover-pages' })).toBeVisible();
    fireEvent.click(within(inspector).getByRole('button', { name: 'Produced artifact Homepage evidence' }));

    expect(screen.getByRole('button', { name: 'Inspect Artifact Homepage evidence · artifact:page' })).toHaveFocus();
    expect(within(inspector).getByRole('heading', { name: 'Homepage evidence' })).toBeVisible();
    fireEvent.click(within(inspector).getByRole('button', { name: 'File desktop.png' }));
    expect(within(inspector).getByText('image/png')).toBeVisible();
    expect(within(inspector).getByText('1,024 bytes')).toBeVisible();
    expect(within(inspector).getByText('a'.repeat(64))).toBeVisible();
    screen.getByRole('button', { name: 'Clear selection' }).focus();
    fireEvent.click(screen.getByRole('button', { name: 'Clear selection' }));
    expect(screen.getByRole('button', { name: 'Inspect File desktop.png · file:page:desktop' })).toHaveFocus();
    expect(within(inspector).getByText('Select a task, artifact or file to inspect it.')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Zoom out' }));
    expect(screen.getByLabelText('Graph zoom')).toHaveTextContent('75%');
    fireEvent.click(screen.getByRole('button', { name: 'Reset view' }));
    expect(screen.getByLabelText('Graph zoom')).toHaveTextContent('100%');
  });

  it('retains selection and zoom as work changes and clears a removed selection', () => {
    const view = render(<ExecutionGraph graph={graph} />);
    fireEvent.click(screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' }));
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    const changed: OperationGraph = { ...graph, nodes: graph.nodes.map(node => node.kind === 'task' && node.task.id === 'child'
      ? { ...node, task: { ...node.task, status: 'failed', error: 'Browser exited before capture' } } : node) };

    view.rerender(<ExecutionGraph graph={changed} />);

    expect(screen.getByLabelText('Graph zoom')).toHaveTextContent('125%');
    expect(screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(screen.getByRole('region', { name: 'Node details' })).getByText('Browser exited before capture')).toBeVisible();
    view.rerender(<ExecutionGraph graph={{ ...graph, nodes: [graph.nodes[0]], edges: [] }} />);
    expect(screen.getByText('Select a task, artifact or file to inspect it.')).toBeVisible();
  });

  it('shows empty work and rejects cyclic graphs without crashing the page', () => {
    const view = render(<ExecutionGraph graph={{ ...graph, nodes: [], edges: [] }} />);
    expect(screen.getByRole('heading', { name: 'No work recorded yet' })).toBeVisible();
    view.rerender(<ExecutionGraph graph={{ ...graph, edges: [...graph.edges, { source: 'task:child', target: 'task:root', kind: 'parent_child' }] }} />);
    expect(screen.getByRole('alert')).toHaveTextContent('invalid execution graph');
  });
});

describe('operation graph queries', () => {
  it('polls active work and clears failed data before a manual retry', async () => {
    vi.useFakeTimers();
    try {
      let current = graph;
      let failed = false;
      const fetch = vi.fn(async () => failed ? new Response(null, { status: 503 }) : Response.json(current));
      vi.stubGlobal('fetch', fetch);
      await act(async () => { render(<OperationGraphPanel url={url} credentials={credentials} operationID={graph.operation_id} onSignIn={vi.fn()} />); });
      fireEvent.click(screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' }));
      current = { ...graph, status: 'completed', nodes: graph.nodes.map(node => node.kind === 'task'
        ? { ...node, task: { ...node.task, status: 'succeeded' } } : node) };

      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });

      expect(fetch).toHaveBeenCalledTimes(2);
      expect(within(screen.getByRole('region', { name: 'Node details' })).getByText('Succeeded')).toBeVisible();
      failed = true;
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(screen.getByRole('alert')).toHaveTextContent('Could not load the execution graph');
      expect(screen.queryByRole('region', { name: 'Operation execution graph' })).not.toBeInTheDocument();
      failed = false;
      fireEvent.click(screen.getByRole('button', { name: 'Refresh graph' }));
      await act(async () => {});
      expect(screen.getByRole('region', { name: 'Operation execution graph' })).toBeVisible();
    } finally { vi.useRealTimers(); }
  });

  it('does not send credentials before sign-in and stops polling after authentication failure', async () => {
    vi.useFakeTimers();
    try {
      const fetch = vi.fn(async () => new Response(null, { status: 401 }));
      vi.stubGlobal('fetch', fetch);
      const onSignIn = vi.fn();
      const view = render(<OperationGraphPanel url={url} operationID={graph.operation_id} onSignIn={onSignIn} />);
      expect(fetch).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
      expect(onSignIn).toHaveBeenCalledOnce();
      await act(async () => { view.rerender(<OperationGraphPanel url={url} credentials={credentials} operationID={graph.operation_id} onSignIn={onSignIn} />); });
      await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
      expect(fetch).toHaveBeenCalledTimes(1);
      expect(screen.queryByRole('region', { name: 'Operation execution graph' })).not.toBeInTheDocument();
    } finally { vi.useRealTimers(); }
  });

  it('ignores late responses from another connection and removes its diagram immediately', async () => {
    let finish!: (response: Response) => void;
    let signal: AbortSignal | null | undefined;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, options: RequestInit = {}) => {
      if (String(input).startsWith(url)) { signal = options.signal; return new Promise<Response>(resolve => { finish = resolve; }); }
      return Promise.resolve(Response.json({ ...graph, nodes: [], edges: [] }));
    }));
    const view = render(<OperationGraphPanel url={url} credentials={credentials} operationID={graph.operation_id} onSignIn={vi.fn()} />);

    view.rerender(<OperationGraphPanel url="https://other.example.test" credentials={credentials} operationID={graph.operation_id} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('heading', { name: 'No work recorded yet' })).toBeVisible();
    expect(signal?.aborted).toBe(true);
    await act(async () => finish(Response.json(graph)));
    expect(screen.queryByRole('button', { name: 'Inspect Task capture-page · task:child' })).not.toBeInTheDocument();
  });
});

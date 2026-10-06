import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useState } from 'react';
import type { OperationGraph } from '../src/api/graph-types';
import { ExecutionGraph } from '../src/components/ExecutionGraph';
import { OperationGraphPanel } from '../src/components/OperationGraphPanel';
import { Dialog } from '../src/components/Dialog';

const url = 'https://commander.example.test';
const credentials = { url, username: 'operator' };
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
    expect(screen.queryByRole('region', { name: 'Node details' })).not.toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    const taskNode = screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' });
    taskNode.focus();
    fireEvent.click(taskNode);
    expect(screen.getByRole('dialog', { name: 'Node details' })).toBeVisible();
    const inspector = screen.getByRole('region', { name: 'Node details' });
    expect(within(inspector).getByText('agent-two')).toBeVisible();
    expect(within(inspector).getByText('Running')).toBeVisible();
    expect(within(inspector).getByRole('button', { name: 'Parent task discover-pages' })).toBeVisible();
    fireEvent.click(within(inspector).getByRole('button', { name: 'Produced artifact Homepage evidence' }));

    expect(within(inspector).getByRole('heading', { name: 'Homepage evidence' })).toHaveFocus();
    fireEvent.click(within(inspector).getByRole('button', { name: 'File desktop.png' }));
    expect(within(inspector).getByText('image/png')).toBeVisible();
    expect(within(inspector).getByText('1,024 bytes')).toBeVisible();
    expect(within(inspector).getByText('a'.repeat(64))).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Close dialog' }));
    expect(screen.getByRole('button', { name: 'Inspect File desktop.png · file:page:desktop' })).toHaveFocus();
    expect(screen.queryByRole('region', { name: 'Node details' })).not.toBeInTheDocument();
    expect(document.body.style.overflow).not.toBe('hidden');
    fireEvent.click(screen.getByRole('button', { name: 'Zoom out' }));
    expect(screen.getByLabelText('Graph zoom')).toHaveTextContent('75%');
    fireEvent.click(screen.getByRole('button', { name: 'Reset view' }));
    expect(screen.getByLabelText('Graph zoom')).toHaveTextContent('100%');
  });

  it('retains selection and zoom as work changes and clears a removed selection', () => {
    const view = render(<ExecutionGraph graph={graph} />);
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    fireEvent.click(screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' }));
    const changed: OperationGraph = { ...graph, nodes: graph.nodes.map(node => node.kind === 'task' && node.task.id === 'child'
      ? { ...node, task: { ...node.task, status: 'failed', error: 'Browser exited before capture' } } : node) };

    view.rerender(<ExecutionGraph graph={changed} />);

    expect(screen.getByLabelText('Graph zoom')).toHaveTextContent('125%');
    expect(screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(screen.getByRole('region', { name: 'Node details' })).getByText('Browser exited before capture')).toBeVisible();
    view.rerender(<ExecutionGraph graph={{ ...graph, nodes: [graph.nodes[0]], edges: [] }} />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(document.body.style.overflow).not.toBe('hidden');
    expect(screen.getByRole('region', { name: 'Operation execution graph' })).toHaveFocus();
  });

  it('closes with Escape and returns focus to the graph node', () => {
    render(<ExecutionGraph graph={graph} />);
    const node = screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' });
    node.focus();
    fireEvent.click(node);

    fireEvent(screen.getByRole('dialog'), new Event('cancel', { cancelable: true }));

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(node).toHaveFocus();
    expect(document.body.style.overflow).not.toBe('hidden');
  });

  it('closes node details before opening evidence and restores scrolling and focus afterward', () => {
    const opened = vi.fn();
    function Evidence() {
      const [open, setOpen] = useState(false);
      return <><ExecutionGraph graph={graph} onOpenArtifact={(artifactID, fileID) => {
        opened(artifactID, fileID, document.querySelectorAll('dialog[open]').length);
        setOpen(true);
      }} />{open && <Dialog open title="Artifact" onClose={() => setOpen(false)}>Evidence</Dialog>}</>;
    }
    render(<Evidence />);
    const node = screen.getByRole('button', { name: 'Inspect File desktop.png · file:page:desktop' });
    node.focus();
    fireEvent.click(node);
    fireEvent.click(screen.getByRole('button', { name: 'View file' }));

    expect(opened).toHaveBeenCalledWith('page', 'desktop', 0);
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(screen.getByRole('dialog', { name: 'Artifact' })).toBeVisible();
    expect(document.body.style.overflow).toBe('hidden');
    fireEvent.click(screen.getByRole('button', { name: 'Close dialog' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(document.body.style.overflow).not.toBe('hidden');
    expect(node).toHaveFocus();
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
      if (new URL(String(input), window.location.origin).searchParams.get('url') === url) {
        signal = options.signal; return new Promise<Response>(resolve => { finish = resolve; });
      }
      return Promise.resolve(Response.json({ ...graph, nodes: [], edges: [] }));
    }));
    const view = render(<OperationGraphPanel url={url} credentials={credentials} operationID={graph.operation_id} onSignIn={vi.fn()} />);

    view.rerender(<OperationGraphPanel url="https://other.example.test" credentials={{ url: 'https://other.example.test', username: 'operator' }} operationID={graph.operation_id} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('heading', { name: 'No work recorded yet' })).toBeVisible();
    expect(signal?.aborted).toBe(true);
    await act(async () => finish(Response.json(graph)));
    expect(screen.queryByRole('button', { name: 'Inspect Task capture-page · task:child' })).not.toBeInTheDocument();
  });
});

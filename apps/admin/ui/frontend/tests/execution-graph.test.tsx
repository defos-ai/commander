import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useState } from 'react';
import type { OperationGraph } from '../src/api/graph-types';
import type { OperationTask } from '../src/api/operation-types';
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
const taskData: OperationTask[] = [
  { id: 'root', operation_id: graph.operation_id, action_name: 'discover-pages', agent_id: 'agent-one', status: 'succeeded',
    started_at: graph.updated_at, input: { url: 'https://example.test', max_pages: 1 },
    result: { action_name: 'discover-pages', output: { pages: ['https://example.test'] } } },
  { id: 'child', operation_id: graph.operation_id, action_name: 'capture-page', agent_id: 'agent-two', status: 'running',
    started_at: graph.updated_at, input: { url: 'https://example.test', sizes: ['desktop'] },
    result: { action_name: 'capture-page', output: { captures: [{ name: 'desktop.png', width: 1280 }] } } },
];

// jsdom lacks PointerEvent and pointer capture; keep their stand-ins local to pan tests.
function pointer(target: Element, type: string, options: PointerEventInit = {}) {
  const event = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, buttons: type === 'pointerup' ? 0 : 1, ...options });
  Object.defineProperties(event, {
    pointerId: { value: options.pointerId ?? 1 },
    pointerType: { value: options.pointerType ?? 'mouse' },
    isPrimary: { value: options.isPrimary ?? true },
  });
  fireEvent(target, event);
}

function renderPannableGraph() {
  render(<ExecutionGraph graph={graph} />);
  const viewport = screen.getByRole('region', { name: 'Operation execution graph' });
  const node = screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' });
  for (const element of [viewport, ...within(viewport).getAllByRole('button')]) {
    let captured: number | undefined;
    Object.defineProperties(element, {
      setPointerCapture: { value: (id: number) => { captured = id; } },
      hasPointerCapture: { value: (id: number) => captured === id },
      releasePointerCapture: { value: () => { captured = undefined; } },
    });
  }
  return { viewport, node };
}

describe('execution graph', () => {
  it('expands task input and output as JSON and collapses them when navigating to another task', () => {
    render(<ExecutionGraph graph={graph} tasks={taskData} />);
    fireEvent.click(screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' }));
    const inspector = within(screen.getByRole('region', { name: 'Node details' }));
    for (const [label, value] of [['Input', taskData[1].input], ['Output', taskData[1].result!.output]] as const) {
      const summary = inspector.getByText(label, { selector: 'summary' });
      const details = summary.closest('details')!;
      expect(details).not.toHaveAttribute('open');
      expect(details.querySelector('pre')).not.toBeVisible();
      fireEvent.click(summary);
      expect(details.querySelector('pre')).toBeVisible();
      expect(details.querySelector('pre')?.textContent).toBe(JSON.stringify(value, null, 2));
    }

    fireEvent.click(inspector.getByRole('button', { name: 'Parent task discover-pages' }));
    expect(inspector.getByRole('heading', { name: 'discover-pages' })).toHaveFocus();
    for (const [label, value] of [['Input', taskData[0].input], ['Output', taskData[0].result!.output]] as const) {
      const details = inspector.getByText(label, { selector: 'summary' }).closest('details')!;
      expect(details).not.toHaveAttribute('open');
      expect(details.querySelector('pre')?.textContent).toBe(JSON.stringify(value, null, 2));
    }
  });

  it('omits unavailable or unrelated payloads and updates matching null and falsy payloads', () => {
    const view = render(<ExecutionGraph graph={graph} tasks={[
      { ...taskData[1], id: 'other-task' }, { ...taskData[1], operation_id: 'other-operation' },
    ]} />);
    fireEvent.click(screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' }));
    const inspector = within(screen.getByRole('region', { name: 'Node details' }));
    expect(inspector.queryByText('Input', { selector: 'summary' })).not.toBeInTheDocument();
    expect(inspector.queryByText('Output', { selector: 'summary' })).not.toBeInTheDocument();
    view.rerender(<ExecutionGraph graph={graph} tasks={[{ ...taskData[1], input: undefined, result: null }]} />);
    expect(inspector.queryByText('Input', { selector: 'summary' })).not.toBeInTheDocument();
    expect(inspector.queryByText('Output', { selector: 'summary' })).not.toBeInTheDocument();

    view.rerender(<ExecutionGraph graph={graph} tasks={[{ ...taskData[1], input: null,
      result: { action_name: 'capture-page', output: false },
    }]} />);
    const input = inspector.getByText('Input', { selector: 'summary' });
    const output = inspector.getByText('Output', { selector: 'summary' });
    fireEvent.click(input);
    fireEvent.click(output);
    expect(input.closest('details')?.querySelector('pre')).toHaveTextContent('null');
    expect(output.closest('details')?.querySelector('pre')).toHaveTextContent('false');

    view.rerender(<ExecutionGraph graph={graph} tasks={[{ ...taskData[1], input: 0,
      result: { action_name: 'capture-page', output: null },
    }]} />);
    expect(input.closest('details')).toHaveAttribute('open');
    expect(input.closest('details')?.querySelector('pre')).toHaveTextContent('0');
    expect(output.closest('details')).toHaveAttribute('open');
    expect(output.closest('details')?.querySelector('pre')).toHaveTextContent('null');
  });

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

  it.each(['mouse', 'pen'])('pans both axes from a node with %s and keeps the next click usable', pointerType => {
    const { viewport, node } = renderPannableGraph();
    viewport.scrollLeft = 100;
    viewport.scrollTop = 80;
    pointer(node, 'pointerdown', { pointerType, clientX: 200, clientY: 160 });
    pointer(node, 'pointermove', { pointerType, clientX: 170, clientY: 140 });

    expect(viewport.scrollLeft).toBe(130);
    expect(viewport.scrollTop).toBe(100);
    pointer(node, 'pointerup', { pointerType, clientX: 170, clientY: 140 });
    fireEvent.click(node, { detail: 1 });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    pointer(node, 'pointermove', { pointerType, clientX: 140, clientY: 110 });
    expect(viewport.scrollLeft).toBe(130);
    expect(viewport.scrollTop).toBe(100);

    pointer(node, 'pointerdown', { pointerType, clientX: 170, clientY: 140 });
    pointer(node, 'pointerup', { pointerType, clientX: 170, clientY: 140 });
    fireEvent.click(node, { detail: 1 });
    expect(screen.getByRole('dialog', { name: 'Node details' })).toBeVisible();
  });

  it('opens a node when a click moves less than the drag threshold', () => {
    const { viewport, node } = renderPannableGraph();
    pointer(node, 'pointerdown', { clientX: 200, clientY: 160 });
    pointer(node, 'pointermove', { clientX: 198, clientY: 159 });
    pointer(node, 'pointerup', { clientX: 198, clientY: 159 });
    fireEvent.click(node, { detail: 1 });

    expect(viewport.scrollLeft).toBe(0);
    expect(viewport.scrollTop).toBe(0);
    expect(screen.getByRole('dialog', { name: 'Node details' })).toBeVisible();
  });

  it.each(['pointercancel', 'lostpointercapture'])('ends background panning on %s and preserves keyboard activation', event => {
    const { viewport, node } = renderPannableGraph();
    pointer(viewport, 'pointerdown', { clientX: 200, clientY: 160 });
    pointer(viewport, 'pointermove', { clientX: 160, clientY: 130 });
    pointer(viewport, event);
    pointer(viewport, 'pointermove', { clientX: 120, clientY: 100 });

    expect(viewport.scrollLeft).toBe(40);
    expect(viewport.scrollTop).toBe(30);
    fireEvent.click(node, { detail: 0 });
    expect(screen.getByRole('dialog', { name: 'Node details' })).toBeVisible();
  });

  it('allows a touch tap after a cancelled mouse drag', () => {
    const { viewport, node } = renderPannableGraph();
    pointer(viewport, 'pointerdown', { clientX: 200, clientY: 160 });
    pointer(viewport, 'pointermove', { clientX: 160, clientY: 130 });
    pointer(viewport, 'pointercancel');

    pointer(node, 'pointerdown', { pointerType: 'touch', pointerId: 2, clientX: 160, clientY: 130 });
    pointer(node, 'pointerup', { pointerType: 'touch', pointerId: 2, clientX: 160, clientY: 130 });
    fireEvent.click(node, { detail: 1 });

    expect(screen.getByRole('dialog', { name: 'Node details' })).toBeVisible();
  });

  it.each([
    { pointerType: 'touch', button: 0 },
    { pointerType: 'mouse', button: 2 },
  ])('leaves $pointerType button $button gestures alone', options => {
    const { viewport } = renderPannableGraph();
    pointer(viewport, 'pointerdown', { ...options, clientX: 200, clientY: 160 });
    pointer(viewport, 'pointermove', { ...options, clientX: 160, clientY: 130 });
    pointer(viewport, 'pointerup', { ...options, clientX: 160, clientY: 130 });

    expect(viewport.scrollLeft).toBe(0);
    expect(viewport.scrollTop).toBe(0);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
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
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
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

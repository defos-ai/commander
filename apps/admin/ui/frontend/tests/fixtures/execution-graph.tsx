import { render, screen, within } from '@testing-library/react';
import type { OperationGraph } from '../../src/api/graph-types';
import type { OperationTask } from '../../src/api/operation-types';
import { ExecutionGraph } from '../../src/components/ExecutionGraph';
import { installPointerCapture } from '../fakes/pointer';

export const url = 'https://commander.example.test';

export const credentials = { url, username: 'operator' };

export const graph: OperationGraph = { operation_id: 'operation-1', status: 'running', updated_at: '2026-10-05T10:00:00Z', nodes: [
  { id: 'task:root', kind: 'task', task: { id: 'root', action_name: 'discover-pages', status: 'succeeded', agent_id: 'agent-one' } },
  { id: 'task:child', kind: 'task', task: { id: 'child', parent_task_id: 'root', action_name: 'capture-page', status: 'running', agent_id: 'agent-two' } },
  { id: 'artifact:page', kind: 'artifact', artifact: { id: 'page', name: 'Homepage evidence', agent_id: 'agent-two', action_name: 'capture-page', created_at: '2026-10-05T10:00:00Z' } },
  { id: 'file:page:desktop', kind: 'file', file: { id: 'desktop', artifact_id: 'page', name: 'desktop.png', media_type: 'image/png', size: 1024, sha256: 'a'.repeat(64) } },
], edges: [
  { source: 'task:root', target: 'task:child', kind: 'parent_child' },
  { source: 'task:child', target: 'artifact:page', kind: 'produced' },
  { source: 'artifact:page', target: 'file:page:desktop', kind: 'contains' },
] };

export const taskData: OperationTask[] = [
  { id: 'root', operation_id: graph.operation_id, action_name: 'discover-pages', agent_id: 'agent-one', status: 'succeeded',
    started_at: graph.updated_at, input: { url: 'https://example.test', max_pages: 1 },
    result: { action_name: 'discover-pages', output: { pages: ['https://example.test'] } } },
  { id: 'child', operation_id: graph.operation_id, action_name: 'capture-page', agent_id: 'agent-two', status: 'running',
    started_at: graph.updated_at, input: { url: 'https://example.test', sizes: ['desktop'] },
    result: { action_name: 'capture-page', output: { captures: [{ name: 'desktop.png', width: 1280 }] } } },
];

export function renderPannableGraph() {
  render(<ExecutionGraph graph={graph} />);
  const viewport = screen.getByRole('region', { name: 'Operation execution graph' });
  const node = screen.getByRole('button', { name: 'Inspect Task capture-page · task:child' });
  installPointerCapture([viewport, ...within(viewport).getAllByRole('button')]);
  return { viewport, node };
}

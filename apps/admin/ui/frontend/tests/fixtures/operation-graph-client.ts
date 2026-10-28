import type { OperationGraph } from '../../src/api/graph-types';

export const url = 'https://commander.example/team';

export const credentials = { url, username: 'operator' };

export const timestamp = '2026-10-05T12:00:00.123456789Z';

export const graph: OperationGraph = {
  operation_id: 'operation-1', status: 'running', updated_at: timestamp,
  nodes: [
    { id: 'task:task-1', kind: 'task', task: { id: 'task-1', action_name: 'discover-pages', status: 'succeeded', agent_id: 'agent-1', command_id: 'command-1', started_at: timestamp, finished_at: timestamp } },
    { id: 'task:task-2', kind: 'task', task: { id: 'task-2', parent_task_id: 'task-1', action_name: 'capture-page', status: 'succeeded', started_at: timestamp, finished_at: timestamp } },
    { id: 'artifact:artifact-1', kind: 'artifact', artifact: { id: 'artifact-1', name: 'Screenshot', agent_id: 'agent-1', action_name: 'capture-page', created_at: timestamp } },
    { id: 'file:artifact-1:file-1', kind: 'file', file: { artifact_id: 'artifact-1', id: 'file-1', name: 'desktop.png', media_type: 'image/png', size: 2048, sha256: 'abc' } },
  ],
  edges: [
    { source: 'task:task-1', target: 'task:task-2', kind: 'parent_child' },
    { source: 'task:task-2', target: 'artifact:artifact-1', kind: 'produced' },
    { source: 'artifact:artifact-1', target: 'file:artifact-1:file-1', kind: 'contains' },
  ],
};

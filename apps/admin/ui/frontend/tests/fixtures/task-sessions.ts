import type { Operation, OperationTask } from '../../src/api/operation-types';

export const scopeID = '1ce89818-8e0e-45be-9065-d663951a2ead';

export const sessionID = '2d243bb5-4645-40a3-9201-ad53cf2f29be';

export const owner = 'browser-agent-1234abcd';

export const timestamp = '2026-10-27T10:00:00Z';

export const task: OperationTask = {
  id: 'task-1', operation_id: 'operation-1', agent_id: '', action_name: 'capture-page',
  status: 'queued', started_at: timestamp, session_intent: { mode: 'use', scope_id: scopeID, id: sessionID },
};

export const placement = { task_id: task.id, owner_agent_id: owner, reason: 'Session owner is unavailable' };

export const operation: Operation = {
  id: task.operation_id, mission: { name: 'review', objectives: ['Review page'], required_actions: [], starting_actions: [], input_schema: {}, output_schema: {} },
  input: {}, status: 'running', tasks: [task], questions: [], events: [], created_at: timestamp, updated_at: timestamp,
  progress: { task_counts: { queued: 1 }, total_tasks: 1, finished_tasks: 0, pending_decisions: 0, pending_questions: 0, settled: false },
  preflight: { ready: true, version: 1, actions: [], tasks: [placement] },
};

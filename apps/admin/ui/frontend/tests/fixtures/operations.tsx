import type { Operation, OperationTask } from '../../src/api/operation-types';
import { mockAPI as fakeAPI } from '../fakes/operations';

export const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };

export const credentials = { username: 'operator', url: connection.url };

export const now = '2026-10-05T10:00:00Z';

export const task: OperationTask = {
  id: 'task-discovery', operation_id: 'operation-review', agent_id: 'web-agent-1234abcd',
  action_name: 'discover-pages', status: 'succeeded', input: { url: 'https://defos.ai' },
  started_at: now, finished_at: now, result: { action_name: 'discover-pages', output: { pages: ['https://defos.ai'] } },
};

export const operation: Operation = {
  id: 'operation-review', mission: { name: 'review-website-with-browser', objectives: ['Review website quality.'],
    required_actions: ['discover-pages'], starting_actions: ['discover-pages'], input_schema: {}, output_schema: {} },
  input: { url: 'https://defos.ai' }, status: 'planning', tasks: [task], questions: [], events: [], created_at: now, updated_at: now,
  progress: { task_counts: { succeeded: 1 }, total_tasks: 1, finished_tasks: 1, pending_decisions: 0, pending_questions: 0, settled: false },
};

export const secretWait: Operation = { ...operation, status: 'waiting', preflight: { ready: false, version: 1, actions: [
  { action_name: 'build', unavailable_agents: [
    { agent_id: 'builder-agent-1234abcd', agent_type: 'builder', reason: 'Action has missing requirements',
      missing_requirements: [{ kind: 'secret', name: 'TOKEN' }] },
    { agent_id: 'builder-agent-2345abcd', agent_type: 'builder', reason: 'Action has missing requirements', missing_requirements: [{ kind: 'secret', name: 'TOKEN' }] },
  ] },
] } };

export function mockAPI(respond?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ operation }, respond);
}

import type { OperationLog, OperationLogPage } from '../../src/api/operation-logs';

export const url = 'https://commander.example/team';
export const credentials = { url, username: 'operator' };
export const operationID = '11111111-1111-4111-8111-111111111111';
export const otherOperationID = '22222222-2222-4222-8222-222222222222';
export const taskID = '33333333-3333-4333-8333-333333333333';
export const otherTaskID = '44444444-4444-4444-8444-444444444444';
export const agentID = 'builder-agent-c20dc9a9';
export const otherAgentID = 'builder-agent-12345678';
export const cursorID = '99999999-9999-4999-8999-999999999999';
export const nextLogID = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa';
export const yearZeroTimestamp = '0000-02-29T12:00:00.123456789Z';

export function makeLog(patch: Partial<OperationLog> = {}): OperationLog {
  return {
    id: 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', agent_id: agentID,
    command_id: '55555555-5555-4555-8555-555555555555', action_name: 'build-agent',
    operation_id: operationID, task_id: taskID, step_id: 'build',
    message: 'Go tests started', created_at: '2026-10-28T12:00:00.123456789Z',
    ...patch,
  };
}

export function makePage(...items: OperationLog[]): OperationLogPage {
  return { items: items.map(item => ({ ...item })), next_cursor: items.at(-1)?.id ?? '' };
}

export const invalidLogPatches: Record<string, unknown>[] = [
  { id: 'not-a-uuid' }, { id: 'EEEEEEEE-EEEE-4EEE-8EEE-EEEEEEEEEEEE' },
  { id: null }, { agent_id: 'builder' }, { agent_id: 'Builder-agent-c20dc9a9' },
  { agent_id: `${'b'.repeat(49)}-agent-12345678` }, { command_id: '' },
  { command_id: '55555555555545558555555555555555' }, { action_name: '' },
  { action_name: '\t\n\u0085\u2000' }, { action_name: '\ud800' },
  { operation_id: otherOperationID }, { operation_id: '' }, { operation_id: null },
  { task_id: '' }, { task_id: null }, { task_id: 'task-1' },
  { step_id: 42 }, { step_id: null }, { step_id: '\udc00' },
  { message: '' }, { message: '\t\n\u0085\u2000' }, { message: null },
  { message: '\ud800' }, { message: '🛠'.repeat(512) + 'a' },
  { created_at: 'not-a-date' }, { created_at: '2026-10-28' },
  { created_at: '2026-02-29T12:00:00Z' }, { created_at: '2026-10-28T24:00:00Z' },
  { created_at: '2026-10-28T12:00:60Z' }, { created_at: '2026-10-28T12:00:00+24:00' },
  { created_at: '2026-10-28T12:00:00.1234567890Z' }, { created_at: '0001-01-01T00:00:00Z' },
];

export const invalidQueries: unknown[] = [
  null, [], { task_id: '' }, { task_id: 'task-1' }, { task_id: null },
  { agent_id: '' }, { agent_id: 'builder' }, { agent_id: null },
  { action_name: '' }, { action_name: '\u0085\n' }, { action_name: '\ud800' }, { action_name: null },
  { order: '' }, { order: 'ASC' }, { order: null }, { cursor: 'not-a-uuid' }, { cursor: null },
  { limit: 0 }, { limit: -1 }, { limit: 1001 }, { limit: 1.5 }, { limit: '10' },
  { limit: NaN }, { limit: Infinity }, { limit: null }, { latest: true },
];

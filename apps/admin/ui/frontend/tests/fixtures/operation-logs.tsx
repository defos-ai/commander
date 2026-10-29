import type { ComponentProps } from 'react';
import { render } from '@testing-library/react';
import { vi } from 'vitest';
import type { OperationLog } from '../../src/api/operation-logs';
import type { OperationTask } from '../../src/api/operation-types';
import { OperationLogsPanel } from '../../src/components/OperationLogsPanel';

export const logURL = 'https://commander.example.test';
export const logCredentials = { url: logURL, username: 'operator' };
export const logOperationID = '10000000-0000-4000-8000-000000000001';
export const otherLogOperationID = '10000000-0000-4000-8000-000000000002';
export const logTaskID = '20000000-0000-4000-8000-000000000001';
export const otherLogTaskID = '20000000-0000-4000-8000-000000000002';

export function operationLog(index: number, change: Partial<OperationLog> = {}): OperationLog {
  return {
    id: `30000000-0000-4000-8000-${index.toString(16).padStart(12, '0')}`,
    agent_id: 'builder-agent-12345678', command_id: '40000000-0000-4000-8000-000000000001',
    action_name: 'build-agent', operation_id: logOperationID, task_id: logTaskID,
    message: `Progress ${index}`, created_at: new Date(Date.UTC(2026, 9, 29, 12, 0, 0) - index * 1000).toISOString(),
    ...change,
  };
}

export function operationLogs(first: number, last: number) {
  return Array.from({ length: last - first + 1 }, (_, index) => operationLog(first + index));
}

export function logTasks(): OperationTask[] {
  return [logTaskID, otherLogTaskID].map((id, index) => ({
    id, operation_id: logOperationID, agent_id: 'builder-agent-12345678', action_name: index ? 'publish-agent' : 'build-agent',
    status: 'succeeded', started_at: '2026-10-29T12:00:00Z', finished_at: '2026-10-29T12:01:00Z',
  }));
}

export function renderOperationLogs(change: Partial<ComponentProps<typeof OperationLogsPanel>> = {}) {
  const props: ComponentProps<typeof OperationLogsPanel> = {
    url: logURL, credentials: logCredentials, operationID: logOperationID, tasks: logTasks(), onSignIn: vi.fn(), ...change,
  };
  return { ...render(<OperationLogsPanel {...props} />), props };
}

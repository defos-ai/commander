import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Operation, OperationTask } from '../src/api/operation-types';
import type { OperationGraph } from '../src/api/graph-types';
import { isOperation } from '../src/api/operations';
import { OperationDetails, TaskSessions } from '../src/components/OperationDetails';
import { GraphNodeDetails } from '../src/components/GraphNodeDetails';

const scopeID = '1ce89818-8e0e-45be-9065-d663951a2ead';
const sessionID = '2d243bb5-4645-40a3-9201-ad53cf2f29be';
const owner = 'browser-agent-1234abcd';
const timestamp = '2026-10-27T10:00:00Z';
const task: OperationTask = {
  id: 'task-1', operation_id: 'operation-1', agent_id: '', action_name: 'capture-page',
  status: 'queued', started_at: timestamp, session_intent: { mode: 'use', scope_id: scopeID, id: sessionID },
};
const placement = { task_id: task.id, owner_agent_id: owner, reason: 'Session owner is unavailable' };
const operation: Operation = {
  id: task.operation_id, mission: { name: 'review', objectives: ['Review page'], required_actions: [], starting_actions: [], input_schema: {}, output_schema: {} },
  input: {}, status: 'running', tasks: [task], questions: [], events: [], created_at: timestamp, updated_at: timestamp,
  progress: { task_counts: { queued: 1 }, total_tasks: 1, finished_tasks: 0, pending_decisions: 0, pending_questions: 0, settled: false },
  preflight: { ready: true, version: 1, actions: [], tasks: [placement] },
};

describe('task session context', () => {
  it('shows placement blockers even when action preflight is ready and navigates using saved session intent', () => {
    const onOpenSession = vi.fn();
    render(<OperationDetails operation={operation} onOpenSession={onOpenSession} />);
    const readiness = screen.getByRole('heading', { name: 'Agent readiness' }).closest('section')!;
    expect(within(readiness).getByText('All required actions have a ready agent in this snapshot.')).toBeVisible();
    expect(within(readiness).getByText(placement.reason)).toBeVisible();
    expect(within(readiness).getByText(owner)).toBeVisible();
    fireEvent.click(screen.getByText(task.action_name).closest('summary')!);
    expect(screen.getByText(scopeID)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Open session' }));
    expect(onOpenSession).toHaveBeenCalledWith({ id: sessionID, scope_id: scopeID, owner_agent_id: owner });
  });

  it('preserves rejected automatic intent and never infers a session from arbitrary action data', () => {
    const invalid = { ...task, session_intent: { mode: '', scope_id: '', error: 'Action contract unavailable' },
      input: { session_id: sessionID, session_scope: scopeID }, result: { action_name: task.action_name, output: { session_id: sessionID } } };
    expect(isOperation({ ...operation, tasks: [invalid] })).toBe(true);
    render(<TaskSessions task={invalid} onOpenSession={vi.fn()} />);
    expect(screen.getByRole('status')).toHaveTextContent('Action contract unavailable');
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(screen.queryByText(sessionID)).not.toBeInTheDocument();
  });

  it('uses the typed raw result reference without requiring lifecycle disposition', () => {
    const onOpenSession = vi.fn();
    const reported = { id: sessionID, scope_id: scopeID, owner_agent_id: owner };
    const completed = { ...task, session_intent: undefined, status: 'succeeded',
      result: { action_name: task.action_name, output: {}, session: reported } };
    expect(isOperation({ ...operation, tasks: [completed] })).toBe(true);
    render(<TaskSessions task={completed} onOpenSession={onOpenSession} />);
    expect(screen.getByText(/records a session observation/)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Open session' }));
    expect(onOpenSession).toHaveBeenCalledWith(reported);
  });

  it('opens a scope-filtered list when accepted intent has no session ID yet', () => {
    const onOpenSession = vi.fn();
    render(<TaskSessions task={{ ...task, session_intent: { mode: 'create', scope_id: scopeID } }} onOpenSession={onOpenSession} />);
    fireEvent.click(screen.getByRole('button', { name: 'View sessions' }));
    expect(onOpenSession).toHaveBeenCalledWith({ id: undefined, scope_id: scopeID, owner_agent_id: undefined });
  });

  it('uses hydrated task metadata in graph details and omits navigation without it', () => {
    const onOpenSession = vi.fn();
    const node = { id: task.id, kind: 'task' as const, task: { id: task.id, action_name: task.action_name, status: task.status } };
    const graph: OperationGraph = { operation_id: operation.id, status: operation.status, updated_at: timestamp, nodes: [node], edges: [] };
    const props = { graph, node, onSelect: vi.fn(), onOpenSession };
    const view = render(<GraphNodeDetails {...props} />);
    expect(screen.queryByRole('button', { name: 'Open session' })).not.toBeInTheDocument();
    view.rerender(<GraphNodeDetails {...props} taskData={task} placement={placement} />);
    expect(screen.getByText(placement.reason)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Open session' }));
    expect(onOpenSession).toHaveBeenCalledWith({ id: sessionID, scope_id: scopeID, owner_agent_id: owner });
  });

  it.each([
    { session_intent: { mode: 'use', scope_id: 'invalid', id: sessionID } },
    { session_intent: { mode: 'replace', scope_id: scopeID, id: sessionID } },
    { session_intent: { mode: 'use', scope_id: scopeID, id: 42 } },
    { session_intent: { mode: '', scope_id: '', error: 42 } },
    { result: { action_name: task.action_name, output: {}, session: { id: sessionID, scope_id: scopeID, owner_agent_id: 'wrong' } } },
  ])('rejects malformed optional session metadata: %j', patch => {
    expect(isOperation({ ...operation, tasks: [{ ...task, ...patch }] })).toBe(false);
  });

  it('rejects malformed task placements without changing the established operation shape', () => {
    expect(isOperation({ ...operation, preflight: { ...operation.preflight, tasks: [{ ...placement, reason: 42 }] } })).toBe(false);
    expect(isOperation({ ...operation, preflight: { ...operation.preflight, tasks: [{ ...placement, owner_agent_id: 'wrong' }] } })).toBe(false);
    expect(isOperation({ ...operation, tasks: [{ ...task, session_intent: undefined }], preflight: { ...operation.preflight, tasks: undefined } })).toBe(true);
  });
});

import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { OperationGraph } from '../src/api/graph-types';
import { isOperation } from '../src/api/operations';
import { GraphNodeDetails } from '../src/components/GraphNodeDetails';
import { OperationDetails, TaskSessions } from '../src/components/OperationDetails';
import { operation, owner, placement, scopeID, sessionID, task, timestamp } from './fixtures/task-sessions';

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

import type { Operation, OperationTask } from '../../src/api/operation-types';
import { fixture as fakeAPI } from '../fakes/operation-controls';
import { now, operation as initialOperation } from './operation-controls';
import { completionProposal } from './operation-client';

export { connection, credentials, detail, view } from './operation-controls';

export const operation: Operation = {
  ...initialOperation, status: 'planning', questions: [],
  mission: {
    ...initialOperation.mission, completion: { decision_by: 'operator' },
    output_schema: { type: 'object', required: ['summary'], properties: { summary: { type: 'string' } } },
  },
  progress: { ...initialOperation.progress, pending_questions: 0 },
};

export function task(status: string): OperationTask {
  return { id: 'task-review', operation_id: operation.id, agent_id: 'review-agent-00000001',
    action_name: 'review', status, started_at: now };
}

export function completed(current: Operation, output: unknown): Operation {
  return { ...structuredClone(current), status: 'completed', completed_by: 'operator', report_id: 'report-review', output,
    progress: { ...current.progress, settled: current.progress.finished_tasks === current.progress.total_tasks } };
}

export function fixture(post?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ operation, mission: operation.mission }, post);
}

export function proposedOperation(output: unknown, id = completionProposal.id): Operation {
  return { ...structuredClone(operation), completion_proposal: { ...completionProposal, id, output } };
}

export function proposalDocument(output: string): string {
  return JSON.stringify(proposedOperation('raw-output')).replace('"output":"raw-output"', `"output":${output}`);
}

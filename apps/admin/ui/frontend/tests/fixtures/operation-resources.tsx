import { fireEvent, screen } from '@testing-library/react';
import type { Operation, OperationArtifact } from '../../src/api/operation-types';
import { fixture as fakeAPI } from '../fakes/operation-resources';

export const connection = { id: 'local', name: 'Commander', url: 'https://commander.example.test' };

export const credentials = { username: 'operator', url: connection.url };

export const now = '2026-10-06T10:00:00Z';

export const artifact: OperationArtifact = { id: 'artifact-page', name: 'Homepage evidence', agent_id: 'agent-one', action_name: 'capture-page', created_at: now,
  files: [{ id: 'file-page', name: 'desktop.png', media_type: 'image/png', size: 1024, sha256: 'a'.repeat(64) }] };

export const operation: Operation = { id: 'operation-review', status: 'completed', report_id: 'report-review', input: {},
  progress: { task_counts: { succeeded: 1 }, total_tasks: 1, finished_tasks: 1, pending_decisions: 0, pending_questions: 0, settled: true },
  mission: { name: 'review-website-with-browser', objectives: ['Review website quality.'], required_actions: [], starting_actions: [], input_schema: {}, output_schema: {} },
  tasks: [{ id: 'task-capture', operation_id: 'operation-review', agent_id: 'agent-one', action_name: 'capture-page', status: 'succeeded', started_at: now,
    result: { action_name: 'capture-page', output: {}, artifacts: [artifact] } }], questions: [], events: [], created_at: now, updated_at: now };

export const report = { id: 'report-review', operation_id: operation.id, mission: operation.mission.name, output: { summary: 'The reviewed pages load correctly.' }, created_at: now };

export async function openOperation() {
  fireEvent.click(await screen.findByRole('button', { name: `View operation ${operation.id}` }));
  await screen.findByText(operation.mission.objectives[0]);
}

export function fixture(respond?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ operation, report, artifact, now }, respond);
}

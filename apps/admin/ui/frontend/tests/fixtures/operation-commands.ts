import type { Operation } from '../../src/api/operation-types';

export const url = 'https://commander.example/team';

export const credentials = { url: url, username: 'operator' };

export const id = '5cde98e0-23d3-447d-ad59-2238b244f689';

export const input = { operation_id: id, mission_name: 'review-website-with-browser', input: { url: 'https://example.com' } };

export const operation: Operation = {
  id, mission: { name: 'review-website-with-browser', objectives: ['Review a website'], required_actions: null, starting_actions: null, input_schema: null, output_schema: null },
  input: input.input, status: 'planning', tasks: null, questions: null, events: null,
  progress: { task_counts: {}, total_tasks: 0, finished_tasks: 0, pending_decisions: 0, pending_questions: 0, settled: false },
  created_at: '2026-10-05T12:00:00Z', updated_at: '2026-10-05T12:00:00Z',
};

export const completedOperation: Operation = {
  ...operation, status: 'completed', completed_by: 'operator', report_id: '6fb2d28f-2bf0-4f17-bf6e-b11279704acd',
  output: { summary: 'Reviewed the saved evidence.' },
};

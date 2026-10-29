import { fireEvent, render, screen } from '@testing-library/react';
import { vi } from 'vitest';
import type { Operation } from '../../src/api/operation-types';
import { OperationsPage } from '../../src/pages/OperationsPage';

export const connection = { id: 'test', name: 'Commander', url: 'https://commander.example.test' };

export const credentials = { username: 'operator', url: connection.url };

export const now = '2026-10-05T10:00:00Z';

export const mission = { name: 'review-website-with-browser', objectives: ['Review website quality.', 'Summarize the findings.'], required_actions: [], starting_actions: [],
  input_schema: { type: 'object', required: ['url'], properties: { url: { type: 'string' } } }, output_schema: {} };

export const operation: Operation = { id: 'operation-review', mission, input: { url: 'https://defos.ai' }, status: 'waiting', tasks: [], events: [],
  progress: { task_counts: {}, total_tasks: 0, finished_tasks: 0, pending_decisions: 0, pending_questions: 1, settled: false },
  questions: [{ id: 'question-browser', status: 'pending', text: 'How should the review continue?', options: ['Configure the browser', 'Use HTTP checks'],
    asked_at: now, fallback_at: '2099-01-01T00:00:00Z' }], created_at: now, updated_at: now };

export function view() { return render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />); }

export async function detail() { fireEvent.click(await screen.findByRole('button', { name: `View operation ${operation.id}` })); await screen.findByText(mission.objectives[0]); }

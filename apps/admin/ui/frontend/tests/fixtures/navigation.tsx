import type { Operation } from '../../src/api/operation-types';
import type { RuntimeSession } from '../../src/api/runtime-sessions';
import { routeHash, type Route } from '../../src/navigation/routes';
import { mockAPI as fakeAPI } from '../fakes/navigation';

export const connection = { id: 'saved-local', name: 'Local Commander', url: 'http://localhost:4274' };

export const mission = { name: 'review-website-with-browser', objectives: ['Review website quality.'], required_actions: ['discover-pages'],
  starting_actions: ['discover-pages'], input_schema: {}, output_schema: {} };

export const operation: Operation = { id: 'operation-review', mission, input: { url: 'https://defos.ai' }, status: 'running',
  progress: { task_counts: {}, total_tasks: 0, finished_tasks: 0, pending_decisions: 0, pending_questions: 0, settled: false },
  tasks: [], questions: [], events: [], created_at: '2026-10-06T10:00:00Z', updated_at: '2026-10-06T10:00:00Z' };

export const resourceSession: RuntimeSession = { id: '11111111-1111-4111-8111-111111111111', scope_id: '22222222-2222-4222-8222-222222222222',
  owner_agent_id: 'shell-agent-12345678', kind: 'shell', label: 'Saved development host', state: 'connected', disposition: 'active',
  busy: false, revision: 1, created_at: '2026-10-06T10:00:00Z', updated_at: '2026-10-06T10:00:00Z' };

export function visit(route: Route) { window.history.replaceState(null, '', routeHash(route)); }

export function mockAPI(snapshot?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ operation, mission, connection, resourceSession }, snapshot);
}

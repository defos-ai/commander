import type { Action, Mission } from '../../src/api/types';
import { mockAPI as fakeAPI, mockEditableMission as editableAPI } from '../fakes/missions';

export const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };

export const credentials = { username: 'operator', url: connection.url };

export const mission: Mission = {
  name: 'review-website-with-browser',
  objectives: ['Review website links.', 'Assess page quality.'],
  required_actions: ['discover-pages', 'check-links'],
  optional_actions: ['capture-page'],
  starting_actions: ['discover-pages'],
  input_schema: {
    type: 'object', required: ['url'],
    properties: { url: { type: 'string', description: 'Website to review' }, max_pages: { type: 'integer', default: 3 } },
  },
  output_schema: { type: 'object', properties: { summary: { type: 'string' } } },
  rules: {
    'aggregate-results': { for_actions: ['discover-pages'] },
    'decide-next-actions': { for_actions: ['check-links', 'capture-page'], params: { decision_by: 'model' } },
    'same-agent': { for_actions: ['discover-pages', 'check-links'] },
  },
};

export const otherMission: Mission = { ...mission, name: 'review-service', objectives: ['Check service availability.'] };

export const registeredActions: Action[] = [
  { name: 'discover-pages', description: 'Discover website pages.', status: 'ready', input_schema: { type: 'object' }, output_schema: { type: 'object' } },
  { name: 'check-links', description: 'Check HTTP links.', status: 'ready', input_schema: { type: 'object' }, output_schema: { type: 'object' } },
  { name: 'capture-page', description: 'Capture a screenshot.', status: 'unavailable', status_reason: 'Browser is not configured.',
    input_schema: { type: 'object' }, output_schema: { type: 'object' } },
];

export function mockAPI(respond?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ registeredActions, mission, otherMission }, respond);
}

export function mockEditableMission(definition = JSON.stringify(mission), failUpdate = false) {
  return editableAPI({ registeredActions, mission, otherMission }, definition, failUpdate);
}

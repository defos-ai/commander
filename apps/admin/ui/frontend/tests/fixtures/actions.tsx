import type { Action } from '../../src/api/types';
import { mockAPI as fakeAPI } from '../fakes/actions';

export const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };

export const credentials = { username: 'operator', url: connection.url };

export const action: Action = {
  name: 'capture-page', description: 'Capture a screenshot of a page.', status: 'unavailable',
  status_reason: 'Chromium and configuration are missing.',
  missing_requirements: [{ kind: 'executable', name: 'chromium' }, { kind: 'configuration', name: 'BROWSER_TOKEN', sensitive: true },
    { kind: 'secret', name: 'SCREENSHOT_TOKEN' }],
  input_schema: { type: 'object', required: ['url'], properties: { url: { type: 'string', description: 'Page to capture' } } },
  output_schema: { type: 'object', properties: { width: { type: 'integer' } } },
};

export const otherAction: Action = { ...action, name: 'check-links', description: 'Check page links.', status: 'unknown', status_reason: undefined, missing_requirements: [] };

export function mockAPI(respond?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ action, otherAction }, respond);
}

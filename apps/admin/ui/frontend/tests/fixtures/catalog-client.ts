import type { Action, Agent } from '../../src/api/types';

export const url = 'https://commander.example/team';

export const credentials = { url, username: 'operator' };

export const action: Action = {
  name: 'capture-page', description: 'Capture a page in the browser', status: 'unavailable',
  status_reason: 'Browser and API key are missing',
  required_secrets: ['BROWSER_API_KEY'],
  missing_requirements: [
    { kind: 'executable', name: 'chromium' },
    { kind: 'secret', name: 'BROWSER_API_KEY' },
    { kind: 'configuration', name: 'LOCAL_BROWSER_KEY', sensitive: true },
  ],
  input_schema: { type: 'object', properties: { url: { type: 'string' } } }, output_schema: {},
};

export const agent: Agent = {
  id: 'browser-agent-0123abcd', type: 'browser-agent', status: 'working', stale: false,
  server: { ip_address: '10.0.0.2', hostname: 'agent-pod-1' },
  work: { action_name: 'capture-page', operation_id: 'operation-1', task_id: 'task-1', command_id: 'command-1' },
  responsibilities: ['Review websites'], actions: [action],
  discovery_scope: { paths: ['/usr/bin'], services: null }, config: { max_pages: 3 },
  observed_at: '2026-10-05T09:00:00.123456789Z', last_seen: '2026-10-05T09:00:01Z',
  last_heartbeat: '2026-10-05T09:00:00Z', lease_expires_at: '2026-10-05T09:00:45Z',
};

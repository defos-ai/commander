import type { Action, Agent } from '../../src/api/types';
import { mockAPI as fakeAPI } from '../fakes/agents';

export const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };

export const credentials = { username: 'operator', url: connection.url };

export const action: Action = {
  name: 'check-accessibility', description: 'Check rendered accessibility.', status: 'ready',
  input_schema: {}, output_schema: {},
};

export const browserAction: Action = {
  ...action, name: 'capture-page', description: 'Capture a rendered page.', status: 'unavailable',
  status_reason: 'Chromium and browser configuration are missing.',
  missing_requirements: [
    { kind: 'executable', name: 'chromium' },
    { kind: 'configuration', name: 'BROWSER_TOKEN', sensitive: true },
    { kind: 'secret', name: 'SCREENSHOT_TOKEN' },
  ],
};

export const agent: Agent = {
  id: 'browser-agent-a1234567', type: 'browser-agent', status: 'working',
  work: { action_name: 'check-accessibility', operation_id: 'operation-123', task_id: 'task-456', step_id: 'step-789', command_id: 'command-012' },
  server: { hostname: 'worker-west-1', ip_address: '10.0.0.12' },
  responsibilities: ['Website quality'], actions: [action, browserAction],
  discovery_scope: null, config: { secret: 'DO-NOT-DISPLAY-CONFIG' },
  observed_at: '2026-10-05T10:01:00Z', last_seen: '2026-10-05T10:01:00Z', last_heartbeat: '2026-10-05T10:00:45Z',
  lease_expires_at: '2026-10-05T10:01:30Z', stale: false,
};

export const staleAgent: Agent = { ...agent, id: 'browser-agent-b1234567', status: 'unavailable', stale: true,
  server: { ...agent.server, hostname: 'worker-east-1' } };

export function mockAPI(respond?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ agent, staleAgent }, respond);
}

import type { Controller } from '../../src/api/types';

export const url = 'https://commander.example/team';

export const credentials = { url, username: 'operator' };

export const controller: Controller = {
  id: 'default-controller-0123abcd', type: 'default', status: 'ready', stale: false,
  server: { hostname: 'control-west-1', os: 'linux', arch: 'amd64' }, version: '0.1.0', agent_selector: 'round-robin',
  rule_selector: 'mission-rules',
  started_at: '2026-10-06T09:00:00Z', last_heartbeat: '2026-10-06T09:10:00.123456789Z', lease_expires_at: '2026-10-06T09:10:45Z',
};

import type { Controller } from '../../src/api/types';
import { mockAPI as fakeAPI } from '../fakes/controllers';

export const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };

export const credentials = { username: 'operator', url: connection.url };

export const controller: Controller = {
  id: 'default-controller-0123abcd', type: 'default', status: 'ready', stale: false,
  server: { hostname: 'control-west-1', os: 'linux', arch: 'amd64' }, version: '0.1.0', agent_selector: 'round-robin',
  rule_selector: 'mission-rules',
  started_at: '2026-10-06T09:00:00Z', last_heartbeat: '2026-10-06T09:10:00Z', lease_expires_at: '2026-10-06T09:10:45Z',
};

export const stale: Controller = { ...controller, id: 'custom-controller-4567efab', type: 'custom', status: 'unavailable', stale: true,
  status_reason: 'Heartbeat lease expired', server: { ...controller.server, hostname: 'control-east-1' } };

export function mockAPI(respond?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ controller, stale }, respond);
}

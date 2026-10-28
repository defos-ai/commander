import {
  type RuntimeSession
} from '../../src/api/runtime-sessions';

export const url = 'https://commander.example/team';

export const credentials = { url, username: 'operator' };

export const id = '5cde98e0-23d3-447d-ad59-2238b244f689';

export const scopeID = 'ee870ebc-2230-49ed-8fc7-24429bcfd05d';

export const requestID = 'f9dfb5e0-5ebf-4928-bcfa-1141d2889ba1';

export const session: RuntimeSession = {
  id, scope_id: scopeID, owner_agent_id: 'browser-agent-0123abcd', kind: 'browser', label: 'Review browser',
  state: 'connected', disposition: 'active', busy: false, revision: 3,
  created_at: '2026-10-27T10:00:00Z', updated_at: '2026-10-27T10:01:00.123456789Z',
  connected_at: '2026-10-27T12:01:00+02:00', verified_at: '2026-10-27T10:01:00Z',
  endpoint: 'ws://runtime.example/session', hostname: 'worker', user: 'operator', peer: 'browser', reason: 'Observed connection',
};

export const serverDetail = 'private server error that must not appear in the UI';

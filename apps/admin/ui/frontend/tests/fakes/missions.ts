import { vi } from 'vitest';
import type { Action, Mission } from '../../src/api/types';
import { upstreamURL } from './proxy';

export function mockAPI(defaults: { registeredActions: Action[]; mission: Mission; otherMission: Mission }, respond?: (url: string, init: RequestInit) => Response | Promise<Response>) {
  const { registeredActions, mission, otherMission } = structuredClone(defaults);

  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    if (upstreamURL(input).endsWith('/api/v1/actions')) return Response.json({ items: registeredActions });
    if (respond) return respond(upstreamURL(input), init);
    return upstreamURL(input).endsWith('/api/v1/missions')
      ? Response.json({ items: [mission, otherMission] }) : Response.json(mission);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

export function mockEditableMission(defaults: { registeredActions: Action[]; mission: Mission; otherMission: Mission }, definition: string, failUpdate = false) {
  let saved = definition;
  return mockAPI(defaults, (url, init) => {
    if (url.endsWith('/api/v1/agents')) return Response.json({ items: [] });
    if (init.method === 'PUT') {
      saved = init.body as string;
      if (failUpdate) throw new TypeError('private-update-response-failed');
    }
    return new Response(saved, { headers: { 'Content-Type': 'application/json' } });
  });
}

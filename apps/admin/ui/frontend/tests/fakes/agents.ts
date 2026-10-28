import { vi } from 'vitest';
import type { Agent } from '../../src/api/types';
import { upstreamURL } from './proxy';

export function mockAPI(defaults: { agent: Agent; staleAgent: Agent }, respond?: (url: string) => Response | Promise<Response>) {
  const { agent, staleAgent } = structuredClone(defaults);

  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    if (respond) return respond(upstreamURL(input));
    return upstreamURL(input).endsWith('/api/v1/agents') ? Response.json({ items: [agent, staleAgent] }) : Response.json(agent);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

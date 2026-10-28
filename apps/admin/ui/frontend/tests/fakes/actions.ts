import { vi } from 'vitest';
import type { Action } from '../../src/api/types';
import { upstreamURL } from './proxy';

export function mockAPI(defaults: { action: Action; otherAction: Action }, respond?: (url: string, init: RequestInit) => Response | Promise<Response>) {
  const { action, otherAction } = structuredClone(defaults);

  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    if (respond) return respond(upstreamURL(input), init);
    return upstreamURL(input).endsWith('/api/v1/actions') ? Response.json({ items: [action, otherAction] })
      : Response.json(upstreamURL(input).endsWith(otherAction.name) ? otherAction : action);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

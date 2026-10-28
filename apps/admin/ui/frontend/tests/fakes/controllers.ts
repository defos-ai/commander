import { vi } from 'vitest';
import type { Controller } from '../../src/api/types';
import { upstreamURL } from './proxy';

export function mockAPI(defaults: { controller: Controller; stale: Controller }, respond?: (url: string, init?: RequestInit) => Response | Promise<Response>) {
  const { controller, stale } = structuredClone(defaults);

  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => respond ? respond(upstreamURL(input), init)
    : upstreamURL(input).endsWith('/api/v1/controllers') ? Response.json({ items: [controller, stale] }) : Response.json(controller));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

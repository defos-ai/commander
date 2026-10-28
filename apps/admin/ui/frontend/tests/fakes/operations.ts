import { vi } from 'vitest';
import type { Operation } from '../../src/api/operation-types';
import { upstreamURL } from './proxy';

export function mockAPI(defaults: { operation: Operation }, respond?: (url: string, options: RequestInit) => Response | Promise<Response>) {
  const { operation } = structuredClone(defaults);

  const fetch = vi.fn(async (input: RequestInfo | URL, options: RequestInit = {}) => respond ? respond(upstreamURL(input), options)
    : Response.json(upstreamURL(input).endsWith('/operations') ? { items: [operation] } : operation));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

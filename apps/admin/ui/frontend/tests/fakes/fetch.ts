import { vi } from 'vitest';

export function respondJSON(body: unknown, status = 200) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => Response.json(body, { status }));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

export function respondSequence(...bodies: unknown[]) {
  const fetch = vi.fn<typeof globalThis.fetch>();
  for (const body of bodies) fetch.mockResolvedValueOnce(Response.json(body));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

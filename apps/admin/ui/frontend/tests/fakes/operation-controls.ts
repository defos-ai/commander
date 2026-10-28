import { vi } from 'vitest';
import type { Operation } from '../../src/api/operation-types';
import type { Mission } from '../../src/api/types';
import { upstreamURL } from './proxy';

interface Defaults {
  operation: Operation;
  mission: Mission;
}

export function fixture(defaults: Defaults, post?: (url: string, body: Record<string, unknown>, signal?: AbortSignal | null) => Response | Promise<Response>) {
  const { operation, mission } = structuredClone(defaults);

  const state = { items: [operation], current: structuredClone(operation) };
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = upstreamURL(input);
    if (init.method === 'POST') return post!(url, JSON.parse(init.body as string) as Record<string, unknown>, init.signal);
    if (url.endsWith('/missions')) return Response.json({ items: [mission] });
    if (url.endsWith('/operations')) return Response.json({ items: state.items });
    return Response.json(state.current);
  });
  vi.stubGlobal('fetch', fetch);
  const snapshot = () => structuredClone(state);
  const change = (update: (draft: typeof state) => void) => {
    const draft = snapshot();
    update(draft);
    Object.assign(state, structuredClone(draft));
  };
  return { snapshot, change, fetch, posts: () => fetch.mock.calls.filter(([, init]) => init?.method === 'POST') };
}

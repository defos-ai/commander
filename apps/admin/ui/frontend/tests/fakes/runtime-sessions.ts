import { vi } from 'vitest';
import type { RuntimeSession } from '../../src/api/runtime-sessions';
import { upstreamURL } from './proxy';

export function fixture(defaults: { session: RuntimeSession; released: RuntimeSession }, respond?: (url: URL, init: RequestInit) => Response | Promise<Response> | undefined) {
  const { session, released } = structuredClone(defaults);

  const state = { items: [session, released], current: session };
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = new URL(upstreamURL(input));
    const custom = respond?.(url, init);
    if (custom !== undefined) return custom;
    if (init.method === 'POST') throw new Error('Unexpected session mutation');
    if (url.pathname === '/api/v1/sessions') return Response.json({ items: state.items });
    if (url.pathname === `/api/v1/sessions/${released.id}`) return Response.json(released);
    return Response.json(state.current);
  });
  vi.stubGlobal('fetch', fetch);
  const snapshot = () => structuredClone(state);
  const change = (update: (draft: typeof state) => void) => {
    const draft = snapshot();
    update(draft);
    Object.assign(state, structuredClone(draft));
  };
  return { snapshot, change, fetch, writes: () => fetch.mock.calls.filter(([, init]) => init?.method === 'POST').map(([url, init]) => [url, init!] as const) };
}

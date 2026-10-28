import { vi } from 'vitest';
import { upstreamURL } from './proxy';

export function mockAPI(defaults: { localURL: "http://localhost:4274" }) {
  const { localURL } = structuredClone(defaults);

  const state: {
    online: boolean;
    authorize: boolean;
    sessions: Map<string, { url: string; username: string }>;
    rejectDelete?: boolean;
    intercept?: (url: string, init: RequestInit) => Promise<Response> | undefined;
  } = { online: true, authorize: true, sessions: new Map() };
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> => {
    const url = upstreamURL(input);
    const address = new URL(String(input), 'http://ui.test');
    const intercepted = state.intercept?.(url, init);
    if (intercepted) return intercepted;
    if (address.pathname === '/ui/connections') {
      if (init.method === 'POST') {
        if (!state.authorize) return Response.json({}, { status: 401 });
        const data = JSON.parse(init.body as string);
        const session = { url: data.url, username: data.username };
        state.sessions.set(data.url, session);
        return Response.json(session);
      }
      if (init.method === 'DELETE') {
        if (state.rejectDelete) return Response.json({}, { status: 502 });
        state.sessions.delete(address.searchParams.get('url')!);
        return new Response(null, { status: 204 });
      }
      return Response.json({ items: [...state.sessions.values()] });
    }
    if (!state.online && !url.startsWith(localURL)) throw new TypeError('Network unavailable');
    if (url.endsWith('/healthz') || url.endsWith('/readyz')) return Response.json({ status: 'ok' });
    if (url.endsWith('/aboutz')) return Response.json({ runtime: 'docker-compose', application: 'commander-api' });
    if (['operations', 'missions', 'controllers', 'agents', 'actions'].some(resource => url.endsWith(`/api/v1/${resource}`))) {
      return state.authorize && state.sessions.has(address.searchParams.get('url') ?? '')
        ? Response.json({ items: [] })
        : Response.json({ error: 'authentication_required' }, { status: 401 });
    }
    throw new Error(`Unexpected test endpoint: ${url}`);
  });
  vi.stubGlobal('fetch', fetch);
  const snapshot = () => ({ ...state, sessions: new Map([...state.sessions].map(([key, value]) => [key, { ...value }])) });
  const change = (update: (draft: typeof state) => void) => {
    const draft = snapshot();
    update(draft);
    Object.assign(state, { ...draft, sessions: new Map([...draft.sessions].map(([key, value]) => [key, { ...value }])) });
  };
  return { snapshot, change, fetch };
}

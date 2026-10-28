import { vi } from 'vitest';
import type { Operation } from '../../src/api/operation-types';
import type { RuntimeSession } from '../../src/api/runtime-sessions';
import type { Mission } from '../../src/api/types';
import type { Connection } from '../../src/connections/types';
import { upstreamURL } from './proxy';

interface Defaults {
  operation: Operation;
  mission: Mission;
  connection: Connection;
  resourceSession: RuntimeSession;
}

export function mockAPI(defaults: Defaults, snapshot: Omit<Operation, 'preflight'> & { preflight?: unknown } = defaults.operation) {
  const { mission, connection, resourceSession } = structuredClone(defaults);

  const observedSnapshot = structuredClone(snapshot);
  let session: { url: string; username: string } | undefined;
  let savedMission = mission;
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = upstreamURL(input);
    if (url === '/ui/connections') {
      if (init.method === 'POST') { const data = JSON.parse(init.body as string); session = { url: data.url, username: data.username }; return Response.json(session); }
      return Response.json({ items: session ? [session] : [] });
    }
    if (url.endsWith('/healthz') || url.endsWith('/readyz')) return Response.json({ status: 'ok' });
    if (!session) return new Response(null, { status: 401 });
    if (url.endsWith('/missions')) {
      if (init.method === 'POST') { savedMission = JSON.parse(init.body as string); return Response.json(savedMission, { status: 201 }); }
      return Response.json({ items: [savedMission] });
    }
    if (url.endsWith(`/missions/${encodeURIComponent(savedMission.name)}`)) return Response.json(savedMission);
    if (url.endsWith('/operations')) return Response.json({ items: [observedSnapshot] });
    if (url.endsWith(`/operations/${observedSnapshot.id}`)) return Response.json(observedSnapshot);
    if (url.startsWith(`${connection.url}/api/v1/operations/${observedSnapshot.id}/logs?`)) {
      return Response.json({ items: [], next_cursor: new URL(url).searchParams.get('cursor') ?? '' });
    }
    if (url.endsWith('/secret-requests') || url.endsWith('/agents')) return Response.json({ items: [] });
    if (url.startsWith(`${connection.url}/api/v1/sessions?`) || url.endsWith('/api/v1/sessions')) return Response.json({ items: [resourceSession] });
    if (url.endsWith(`/api/v1/sessions/${resourceSession.id}`)) return Response.json(resourceSession);
    if (url.endsWith('/graph')) return Response.json({ operation_id: observedSnapshot.id, status: observedSnapshot.status, updated_at: observedSnapshot.updated_at, nodes: [], edges: [] });
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

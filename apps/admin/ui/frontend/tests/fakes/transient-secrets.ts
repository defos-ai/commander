import { vi } from 'vitest';
import type { PendingSecretRequest, TransientSecretRequest } from '../../src/api/secrets';
import { upstreamURL } from './proxy';

type Post = (id: string, value: string, signal?: AbortSignal | null) => Response | Promise<Response>;
type Read = (id: string, signal?: AbortSignal | null) => Response | Promise<Response>;

export function transientSecretsAPI(initial: TransientSecretRequest[], post?: Post, read?: Read, reusable: PendingSecretRequest[] = []) {
  const records = new Map(initial.map(request => [request.id, structuredClone(request)]));
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = new URL(upstreamURL(input));
    const prefix = '/api/v1/secret-requests';
    if (url.pathname === '/api/v1/secrets') return Response.json({ items: [] });
    if (url.pathname === prefix) {
      return Response.json({ items: url.searchParams.get('type') === 'reusable' ? structuredClone(reusable)
        : [...records.values()].filter(record => record.status === 'pending' && Date.parse(record.expires_at) > Date.now()).map(record => structuredClone(record)) });
    }
    const path = url.pathname.slice(prefix.length + 1).split('/');
    const id = path[0];
    if (init.method === 'POST') {
      const value = (JSON.parse(init.body as string) as { value: string }).value;
      return post ? post(id, value, init.signal) : Response.json({ status: 'awaiting_receipt' }, { status: 202 });
    }
    if (read) return read(id, init.signal);
    const record = records.get(id);
    return record ? Response.json(structuredClone(record)) : Response.json({ error: 'secret_request_not_found' }, { status: 404 });
  });
  vi.stubGlobal('fetch', fetch);
  return {
    fetch,
    posts: () => fetch.mock.calls.filter(([, init]) => init?.method === 'POST'),
    change: (id: string, update: Partial<TransientSecretRequest>) => {
      const record = records.get(id);
      if (!record) throw new Error('Missing test secret request');
      records.set(id, { ...structuredClone(record), ...structuredClone(update) });
    },
    read: (id: string) => structuredClone(records.get(id)),
  };
}

import { vi } from 'vitest';
import type { PendingSecretRequest, SecretMetadata } from '../../src/api/secrets';
import { upstreamURL } from './proxy';

interface Defaults {
  pending: PendingSecretRequest;
  secret: SecretMetadata;
  requestsPath: string;
  secretsPath: string;
  secretPath: string;
}

export function fixture(defaults: Defaults, respond?: (path: string, init: RequestInit, url: string) => Response | Promise<Response> | undefined) {
  const { pending, secret, requestsPath, secretsPath, secretPath } = structuredClone(defaults);

  const state = { requests: [pending], secrets: [] as (typeof secret)[], current: undefined as typeof secret | undefined };
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = upstreamURL(input);
    const parsed = new URL(url);
    const path = parsed.pathname + parsed.search;
    const custom = respond?.(path, init, url);
    if (custom !== undefined) return custom;
    if (init.method !== undefined && init.method !== 'GET') throw new Error(`Unexpected mutation: ${init.method} ${path}`);
    if (parsed.pathname === requestsPath) return Response.json({ items: state.requests });
    if (parsed.pathname === '/api/v1/agents') return Response.json({ items: [] });
    if (parsed.pathname === secretsPath) return Response.json({ items: state.secrets });
    if (parsed.pathname === secretPath) return state.current
      ? Response.json(state.current) : Response.json({ error: 'secret_not_found' }, { status: 404 });
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal('fetch', fetch);
  const snapshot = () => structuredClone(state);
  const change = (update: (draft: typeof state) => void) => {
    const draft = snapshot();
    update(draft);
    Object.assign(state, structuredClone(draft));
  };
  return { snapshot, change, fetch, writes: () => fetch.mock.calls.filter(([, init]) => init?.method !== undefined && init.method !== 'GET')
    .map(([url, init]) => [url, init!] as const) };
}

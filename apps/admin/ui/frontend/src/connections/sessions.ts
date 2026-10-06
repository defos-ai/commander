import { normalizeURL } from './client';
import type { Credentials, Session } from './types';

export class SessionError extends Error {
  constructor(readonly kind: 'authentication' | 'forbidden' | 'unavailable' | 'invalid_response') { super(kind); }
}

async function request(path: string, options: RequestInit = {}, signal?: AbortSignal): Promise<Response> {
  const response = await fetch(path, {
    ...options, headers: { Accept: 'application/json', 'X-Commander-UI': '1', ...(options.body ? { 'Content-Type': 'application/json' } : {}) },
    credentials: 'same-origin', redirect: 'error', cache: 'no-store',
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(15_000)]) : AbortSignal.timeout(15_000),
  });
  if (!response.ok) throw new SessionError(response.status === 401 ? 'authentication'
    : response.status === 400 || response.status === 403 ? 'forbidden' : 'unavailable');
  return response;
}

function session(value: unknown): Session {
  if (!value || typeof value !== 'object' || !('url' in value) || typeof value.url !== 'string'
    || !('username' in value) || typeof value.username !== 'string') throw new SessionError('invalid_response');
  return { url: normalizeURL(value.url), username: value.username };
}

export async function listSessions(signal?: AbortSignal): Promise<Session[]> {
  const data = await (await request('/ui/connections', {}, signal)).json();
  if (!Array.isArray(data?.items)) throw new SessionError('invalid_response');
  return data.items.map(session);
}

export async function createSession(url: string, credentials: Credentials, signal?: AbortSignal): Promise<Session> {
  const target = normalizeURL(url);
  const result = session(await (await request('/ui/connections', { method: 'POST', body: JSON.stringify({ url: target, ...credentials }) }, signal)).json());
  if (result.url !== target) throw new SessionError('invalid_response');
  return result;
}

export async function deleteSession(url: string, signal?: AbortSignal): Promise<void> {
  await request(`/ui/connections?${new URLSearchParams({ url: normalizeURL(url) })}`, { method: 'DELETE' }, signal);
}

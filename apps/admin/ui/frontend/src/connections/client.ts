import type { ConnectionCheck, Deployment, Session } from './types';

const REQUEST_TIMEOUT_MS = 5_000;

export function normalizeURL(raw: string): string {
  try {
    const value = raw.trim();
    const url = new URL(value);
    if (
      !/^https?:\/\//i.test(value) ||
      (url.protocol !== 'http:' && url.protocol !== 'https:') ||
      url.username || url.password ||
      /^https?:\/\/[^/\\?#]*@/i.test(value) ||
      value.includes('?') || value.includes('#')
    ) {
      throw new Error('Invalid URL');
    }
    return url.href.replace(/\/+$/, '');
  } catch {
    throw new Error('Invalid URL');
  }
}

interface CheckResponse {
  status: number;
  body: unknown;
}

async function request(url: string, signal: AbortSignal): Promise<CheckResponse | null> {
  try {
    const response = await fetch(url, {
      method: 'GET',
      headers: { Accept: 'application/json', 'X-Commander-UI': '1' },
      credentials: 'same-origin',
      redirect: 'error',
      cache: 'no-store',
      signal,
    });
    return {
      status: response.status,
      body: response.ok ? await response.json() : null,
    };
  } catch {
    return null;
  }
}

function isHealthy(response: CheckResponse | null): boolean {
  return response !== null && response.status >= 200 && response.status < 300 &&
    typeof response.body === 'object' && response.body !== null &&
    'status' in response.body && response.body.status === 'ok';
}

function isAuthenticated(response: CheckResponse | null): boolean {
  return response !== null && response.status >= 200 && response.status < 300 &&
    typeof response.body === 'object' && response.body !== null &&
    'items' in response.body && Array.isArray(response.body.items);
}

export function proxyURL(url: string, path: string, session?: Session): string {
  const target = normalizeURL(url);
  if (session && normalizeURL(session.url) !== target) throw new Error('Session does not match connection');
  return `/ui/proxy?${new URLSearchParams({ url: target, path })}`;
}

export function expireSession(url: string) {
  window.dispatchEvent(new CustomEvent('commander:session-expired', { detail: normalizeURL(url) }));
}

export async function getDeployment(url: string, signal: AbortSignal): Promise<Deployment | null> {
  const response = await request(proxyURL(url, '/aboutz'), signal);
  const body = response?.status === 200 ? response.body : null;
  if (!body || typeof body !== 'object' || !('runtime' in body) || !('application' in body)) return null;
  if (body.runtime !== 'native' && body.runtime !== 'docker' && body.runtime !== 'docker-compose') return null;
  if (body.application !== 'commander-api' && body.application !== 'commander') return null;
  return { runtime: body.runtime, application: body.application };
}

export async function checkConnection(
  url: string,
  credentials?: Session,
  signal?: AbortSignal,
): Promise<ConnectionCheck> {
  if (signal?.aborted) throw new DOMException('Connection check aborted', 'AbortError');
  const baseURL = normalizeURL(url);
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true });
  const timeout = setTimeout(abort, REQUEST_TIMEOUT_MS);

  try {
    const [health, readiness, authentication] = await Promise.all([
      request(proxyURL(baseURL, '/healthz'), controller.signal),
      request(proxyURL(baseURL, '/readyz'), controller.signal),
      credentials ? request(proxyURL(baseURL, '/api/v1/missions', credentials), controller.signal) : null,
    ]);
    if (signal?.aborted) throw new DOMException('Connection check aborted', 'AbortError');

    if (authentication?.status === 401) expireSession(baseURL);
    const api = isHealthy(health) ? 'ok' : 'unavailable';
    const database = isHealthy(readiness) ? 'ok' : 'unavailable';
    const status = api !== 'ok' ? 'unreachable'
      : !credentials || authentication?.status === 401 ? 'authentication'
        : database !== 'ok' || !isAuthenticated(authentication) ? 'degraded'
          : 'ready';

    return { status, api, database, checkedAt: Date.now() };
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener('abort', abort);
  }
}

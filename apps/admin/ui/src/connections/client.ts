import type { ConnectionCheck, Credentials } from './types';

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

async function request(url: string, signal: AbortSignal, authorization?: string): Promise<CheckResponse | null> {
  try {
    const response = await fetch(url, {
      method: 'GET',
      headers: { Accept: 'application/json', ...(authorization ? { Authorization: authorization } : {}) },
      credentials: 'omit',
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

export function basicAuthorization(credentials: Credentials): string {
  if (credentials.username.includes(':')) throw new Error('Invalid username');
  const bytes = new TextEncoder().encode(`${credentials.username}:${credentials.password}`);
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return `Basic ${btoa(binary)}`;
}

export async function checkConnection(
  url: string,
  credentials?: Credentials,
  signal?: AbortSignal,
): Promise<ConnectionCheck> {
  if (signal?.aborted) throw new DOMException('Connection check aborted', 'AbortError');
  const baseURL = normalizeURL(url);
  const authorization = credentials ? basicAuthorization(credentials) : undefined;
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true });
  const timeout = setTimeout(abort, REQUEST_TIMEOUT_MS);

  try {
    const [health, readiness, authentication] = await Promise.all([
      request(`${baseURL}/healthz`, controller.signal),
      request(`${baseURL}/readyz`, controller.signal),
      authorization ? request(`${baseURL}/api/v1/missions`, controller.signal, authorization) : null,
    ]);
    if (signal?.aborted) throw new DOMException('Connection check aborted', 'AbortError');

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

import { afterEach, describe, expect, it, vi } from 'vitest';
import { checkConnection, normalizeURL } from '../src/connections/client';
import { pendingFetch } from './fakes/pending-fetch';

const baseURL = 'https://commander.example/prefix';
const credentials = { url: baseURL, username: 'üser' };

function requestedPath(input: RequestInfo | URL): string {
  const request = new URL(String(input), 'http://localhost');
  expect(request.origin).toBe('http://localhost');
  expect(request.pathname).toBe('/ui/proxy');
  expect(request.searchParams.get('url')).toBe(baseURL);
  return request.searchParams.get('path')!;
}

function healthyResponse(url: string) {
  return Response.json(requestedPath(url).endsWith('/missions') ? { items: [] } : { status: 'ok' });
}

afterEach(() => vi.useRealTimers());

describe('connection URLs', () => {
  it('normalizes the origin and preserves a reverse-proxy prefix', () => {
    expect(normalizeURL(' HTTPS://COMMANDER.EXAMPLE:443/prefix/// ')).toBe(baseURL);
    expect(normalizeURL('http://localhost:4274/')).toBe('http://localhost:4274');
  });

  it.each([
    'commander.example',
    'ftp://commander.example',
    'https://user:password@commander.example',
    'https://@commander.example',
    'https://commander.example/?token=secret',
    'https://commander.example/?',
    'https://commander.example/#fragment',
    'https://commander.example/#',
  ])('rejects an unsafe or invalid URL: %s', url => {
    expect(() => normalizeURL(url)).toThrow('Invalid URL');
  });
});

describe('connection checks', () => {
  it('uses the same-origin proxy and never sends API credentials from the browser', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async url => healthyResponse(String(url)));
    vi.stubGlobal('fetch', fetch);

    const check = await checkConnection(`${baseURL}/`, credentials);

    expect(check).toMatchObject({ status: 'ready', api: 'ok', database: 'ok', checkedAt: expect.any(Number) });
    expect(fetch.mock.calls.map(([url]) => requestedPath(url))).toEqual([
      '/healthz', '/readyz', `/api/v1/missions`,
    ]);
    for (const [, options] of fetch.mock.calls) {
      expect(options).toMatchObject({ method: 'GET', credentials: 'same-origin', redirect: 'error', cache: 'no-store' });
      expect(new Headers(options?.headers).get('Authorization')).toBeNull();
      expect(new Headers(options?.headers).get('X-Commander-UI')).toBe('1');
    }
  });

  it('checks public health without requesting the protected catalog when signed out', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async url => healthyResponse(String(url)));
    vi.stubGlobal('fetch', fetch);

    expect(await checkConnection(baseURL)).toMatchObject({ status: 'authentication', api: 'ok', database: 'ok' });
    expect(fetch.mock.calls.map(([url]) => requestedPath(url))).toEqual(['/healthz', '/readyz']);
  });

  it.each([
    { failure: 'rejected credentials', path: '/api/v1/missions', response: () => Response.json({}, { status: 401 }), status: 'authentication', api: 'ok', database: 'ok' },
    { failure: 'unavailable database', path: '/readyz', response: () => Response.json({ status: 'unavailable' }, { status: 503 }), status: 'degraded', api: 'ok', database: 'unavailable' },
    { failure: 'forbidden catalog', path: '/api/v1/missions', response: () => Response.json({}, { status: 403 }), status: 'degraded', api: 'ok', database: 'ok' },
    { failure: 'invalid health JSON', path: '/healthz', response: () => new Response('not JSON'), status: 'unreachable', api: 'unavailable', database: 'ok' },
    { failure: 'unexpected health shape', path: '/healthz', response: () => Response.json({ status: true }), status: 'unreachable', api: 'unavailable', database: 'ok' },
    { failure: 'invalid catalog JSON', path: '/api/v1/missions', response: () => new Response('not JSON'), status: 'degraded', api: 'ok', database: 'ok' },
    { failure: 'unexpected catalog shape', path: '/api/v1/missions', response: () => Response.json({ items: {} }), status: 'degraded', api: 'ok', database: 'ok' },
  ])('does not report ready for $failure', async ({ path, response, status, api, database }) => {
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>(async url => requestedPath(url) === path ? response() : healthyResponse(String(url))));

    expect(await checkConnection(baseURL, credentials)).toMatchObject({ status, api, database });
  });

  it('converts network failures into an unreachable state', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError('Failed to fetch')));

    expect(await checkConnection(baseURL, credentials)).toMatchObject({ status: 'unreachable', api: 'unavailable', database: 'unavailable' });
  });

  it('bounds stalled requests to five seconds and reports ordinary timeouts as unavailable', async () => {
    vi.useFakeTimers();
    const fetch = pendingFetch();
    const settled = vi.fn();
    const pending = checkConnection(baseURL, credentials).then(result => { settled(); return result; });

    await vi.advanceTimersByTimeAsync(4_999);
    expect(settled).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);

    expect(await pending).toMatchObject({ status: 'unreachable', api: 'unavailable', database: 'unavailable' });
    expect(fetch.mock.calls.every(([, options]) => options?.signal?.aborted)).toBe(true);
  });

  it('rejects caller cancellation with AbortError and avoids requests for an already aborted check', async () => {
    const fetch = pendingFetch();
    const controller = new AbortController();
    const pending = checkConnection(baseURL, credentials, controller.signal);

    controller.abort();

    await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetch.mock.calls.every(([, options]) => options?.signal?.aborted)).toBe(true);
    fetch.mockClear();
    await expect(checkConnection(baseURL, credentials, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetch).not.toHaveBeenCalled();
  });
});

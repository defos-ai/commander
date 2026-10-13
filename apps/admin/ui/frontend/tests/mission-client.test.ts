import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/api/client';
import { getMission, listMissions } from '../src/api/missions';
import type { Mission } from '../src/api/types';

const url = 'https://commander.example/proxy';
const credentials = { url: url, username: ' opérateur ' };

function requestedPath(input: RequestInfo | URL): string {
  const request = new URL(String(input), 'http://localhost');
  expect(request.origin).toBe('http://localhost');
  expect(request.pathname).toBe('/ui/proxy');
  expect(request.searchParams.get('url')).toBe(url);
  return request.searchParams.get('path')!;
}
const mission: Mission = {
  name: 'review-website-with-browser',
  objective: 'Review a website',
  required_actions: ['discover-pages'],
  starting_actions: ['discover-pages'],
  steps: [{ id: 'discover', objective: 'Find pages', agent_types: null, required_actions: ['discover-pages'] }],
  rules: {
    'aggregate-results': { for_actions: ['discover-pages'] },
    'decide-next-actions': { for_actions: ['check-links'], params: { decision_by: 'model' } },
  },
  input_schema: { type: 'object', properties: { url: { type: 'string' } } },
  output_schema: {},
};

afterEach(() => vi.useRealTimers());

describe('mission API', () => {
  it('reads the catalog and a safely encoded detail URL through the session proxy', async () => {
    const namedMission = { ...mission, name: 'review/website ?#é' };
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(Response.json({ items: [mission] }))
      .mockResolvedValueOnce(Response.json(namedMission));
    vi.stubGlobal('fetch', fetch);

    expect(await listMissions(`${url}/`, credentials)).toEqual([mission]);
    expect(await getMission(url, credentials, namedMission.name)).toEqual(namedMission);

    expect(fetch.mock.calls.map(([path]) => requestedPath(path))).toEqual([
      `/api/v1/missions`, `/api/v1/missions/review%2Fwebsite%20%3F%23%C3%A9`,
    ]);
    for (const [, options] of fetch.mock.calls) {
      expect(options).toMatchObject({ method: 'GET', credentials: 'same-origin', redirect: 'error', cache: 'no-store' });
      expect(new Headers(options?.headers).get('Authorization')).toBeNull();
      expect(new Headers(options?.headers).get('X-Commander-UI')).toBe('1');
      expect(new Headers(options?.headers).get('Accept')).toBe('application/json');
    }
  });

  it('accepts empty catalogs and nullable Go fields without inventing missing data', async () => {
    const nullableMission = {
      name: 'minimal', objective: 'Review', required_actions: null, starting_actions: null,
      input_schema: null, output_schema: null,
    };
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(Response.json({ items: [] }))
      .mockResolvedValueOnce(Response.json(nullableMission)));

    expect(await listMissions(url, credentials)).toEqual([]);
    expect(await getMission(url, credentials, 'minimal')).toEqual(nullableMission);
  });

  it.each([
    [401, 'authentication'], [403, 'forbidden'], [404, 'not_found'],
    [503, 'unavailable'], [504, 'timeout'],
  ])('classifies HTTP %i without exposing error response bodies', async (status, kind) => {
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      Response.json({ error: `Do not display private-upstream-details` }, { status: Number(status) }),
    ));

    const error = await getMission(url, credentials, mission.name).catch(value => value);

    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ kind, message: kind });
    expect(String(error)).not.toContain('private-upstream-details');
  });

  it('converts network and redirect failures into an unavailable error', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError('Failed to fetch')));

    await expect(listMissions(url, credentials)).rejects.toMatchObject({ kind: 'unavailable' });
  });

  it.each([
    { items: null },
    { items: [{ ...mission, required_actions: 'discover-pages' }] },
    { items: [{ ...mission, steps: [{ id: 'discover' }] }] },
    { items: [{ ...mission, rules: [] }] },
    { items: [{ ...mission, rules: { 'aggregate-results': { actions: ['discover-pages'] } } }] },
    { items: [{ ...mission, rules: { 'aggregate-results': { for_actions: ['discover-pages'], actions: ['check-links'] } } }] },
    { items: [{ ...mission, rules: { 'aggregate-results': { for_actions: [42] } } }] },
    { items: [{ ...mission, rules: { 'aggregate-results': { for_actions: [] } } }] },
    { items: [{ ...mission, rules: { 'aggregate-results': { for_actions: ['discover-pages', 'discover-pages'] } } }] },
    { items: [{ ...mission, rules: { 'aggregate-results': { for_actions: ['  '] } } }] },
    { items: [{ ...mission, rules: { 'aggregate-results': { for_actions: ['discover-pages'], params: { decision_by: 'model' } } } }] },
    { items: [{ ...mission, rules: { 'aggregate-results': { for_actions: ['discover-pages'], params: { unknown: true } } } }] },
    { items: [{ ...mission, rules: { 'decide-next-actions': { for_actions: ['check-links'] } } }] },
    { items: [{ ...mission, rules: { 'decide-next-actions': { for_actions: ['check-links'], params: { decision_by: 'unknown' } } } }] },
    { items: [{ ...mission, rules: { 'unknown-rule': { for_actions: ['check-links'] } } }] },
    { items: [{ ...mission, input_schema: [] }] },
  ])('rejects malformed fields before they reach rendering', async body => {
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockResolvedValue(Response.json(body)));

    await expect(listMissions(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it('rejects invalid JSON and a response for a different mission', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(new Response('<html>Proxy page</html>'))
      .mockResolvedValueOnce(Response.json({ ...mission, name: 'another' })));

    await expect(listMissions(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
    await expect(getMission(url, credentials, mission.name)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it('rejects dot segments before requesting a different endpoint', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>();
    vi.stubGlobal('fetch', fetch);

    for (const name of ['.', '..', '']) {
      await expect(getMission(url, credentials, name)).rejects.toMatchObject({ kind: 'invalid_response' });
    }
    expect(fetch).not.toHaveBeenCalled();
  });

  it('propagates caller cancellation and does not send pre-cancelled requests', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>((_url, options) => new Promise((_resolve, reject) => {
      options?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
    }));
    vi.stubGlobal('fetch', fetch);
    const controller = new AbortController();
    const pending = listMissions(url, credentials, controller.signal);

    controller.abort();

    await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
    fetch.mockClear();
    await expect(listMissions(url, credentials, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetch).not.toHaveBeenCalled();
  });

  it('keeps the five-second timeout active while reading the response body', async () => {
    vi.useFakeTimers();
    const fetch = vi.fn<typeof globalThis.fetch>(async (_url, options) => ({
      ok: true,
      json: () => new Promise((_resolve, reject) => {
        options?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
      }),
    } as Response));
    vi.stubGlobal('fetch', fetch);
    const pending = listMissions(url, credentials);
    const result = expect(pending).rejects.toMatchObject({ kind: 'timeout' });

    await vi.advanceTimersByTimeAsync(5_000);

    await result;
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });
});

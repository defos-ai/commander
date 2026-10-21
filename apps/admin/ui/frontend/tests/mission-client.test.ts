import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/api/client';
import { createMission, getMission, listMissions, MissionCreateError } from '../src/api/missions';
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
  objectives: ['Review a website'],
  required_actions: ['discover-pages'],
  optional_actions: ['capture-page'],
  starting_actions: ['discover-pages'],
  steps: [{ id: 'discover', objective: 'Find pages', agent_types: null, required_actions: ['discover-pages'] }],
  rules: {
    'aggregate-results': { for_actions: ['discover-pages'] },
    'decide-next-actions': { for_actions: ['check-links'], params: { decision_by: 'model' } },
    'same-agent': { for_actions: ['discover-pages', 'check-links'] },
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
      name: 'minimal', objectives: ['Review'], required_actions: null, starting_actions: null,
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
    { items: [{ ...mission, objectives: undefined, objective: 'Legacy objective' }] },
    { items: [{ ...mission, objectives: 'Review a website' }] },
    { items: [{ ...mission, objectives: null }] },
    { items: [{ ...mission, objectives: [] }] },
    { items: [{ ...mission, objectives: ['Review', ' \n '] }] },
    { items: [{ ...mission, objectives: ['Review', 42] }] },
    { items: [{ ...mission, required_actions: 'discover-pages' }] },
    { items: [{ ...mission, optional_actions: 'capture-page' }] },
    { items: [{ ...mission, optional_actions: [42] }] },
    { items: [{ ...mission, optional_actions: null }] },
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
    { items: [{ ...mission, rules: { 'same-agent': { for_actions: ['discover-pages'], params: { decision_by: 'model' } } } }] },
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

describe('mission creation', () => {
  const definition = JSON.stringify(mission);

  it('posts the original JSON through the session proxy without rounding schema numbers', async () => {
    const raw = '{\n "name":"new mission", "objectives":["Review"], "input_schema":{"minimum":9007199254740993}, "output_schema":{}, "optional_actions":null, "rules":null\n}';
    const saved = { ...JSON.parse(raw), required_actions: null, starting_actions: null, optional_actions: undefined, rules: undefined };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(Response.json(saved, { status: 201 }));
    vi.stubGlobal('fetch', fetch);
    const stored = vi.spyOn(Storage.prototype, 'setItem');

    expect(await createMission(url, credentials, raw)).toEqual(saved);

    expect(fetch).toHaveBeenCalledTimes(1);
    const [path, options] = fetch.mock.calls[0];
    expect(requestedPath(path)).toBe('/api/v1/missions');
    expect(options).toMatchObject({ method: 'POST', body: raw, credentials: 'same-origin', redirect: 'error', cache: 'no-store' });
    const headers = new Headers(options?.headers);
    expect(headers.get('Content-Type')).toBe('application/json');
    expect(headers.get('Accept')).toBe('application/json');
    expect(headers.get('X-Commander-UI')).toBe('1');
    expect(headers.has('Authorization')).toBe(false);
    expect(stored).not.toHaveBeenCalled();
  });

  it('rejects malformed JSON, missing form fields and mismatched sessions before sending', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>();
    vi.stubGlobal('fetch', fetch);
    await expect(createMission(url, credentials, '{ private-content')).rejects.toEqual(new MissionCreateError('invalid_json'));
    for (const patch of [null, [], { ...mission, name: ' ' }, { ...mission, name: '.' }, { ...mission, name: '..' },
      { ...mission, objectives: undefined, objective: 'Legacy objective' },
      { ...mission, objectives: 'Review' }, { ...mission, objectives: null }, { ...mission, objectives: [] },
      { ...mission, objectives: ['Review', ' \n '] }, { ...mission, objectives: ['Review', 42] },
      { ...mission, objectives: [''] }, { ...mission, input_schema: null }, { ...mission, output_schema: [] }]) {
      await expect(createMission(url, credentials, JSON.stringify(patch))).rejects.toEqual(new MissionCreateError('invalid_input'));
    }
    await expect(createMission('https://other.example', credentials, definition)).rejects.toEqual(new MissionCreateError('invalid_input'));
    expect(fetch).not.toHaveBeenCalled();
  });

  it('lets the server validate optional fields and cross-field constraints', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response('private-validation-details', { status: 400 }));
    vi.stubGlobal('fetch', fetch);
    const raw = JSON.stringify({ ...mission, rules: { unknown: {} }, starting_actions: ['not-required'] });

    await expect(createMission(url, credentials, raw)).rejects.toEqual(new MissionCreateError('invalid_input'));
    expect(fetch.mock.calls[0][1]?.body).toBe(raw);
  });

  it.each([
    [400, 'invalid_input'], [413, 'invalid_input'], [415, 'invalid_input'], [422, 'invalid_input'],
    [401, 'authentication'], [403, 'forbidden'], [409, 'conflict'], [408, 'unknown'], [500, 'unknown'], [503, 'unknown'],
  ] as const)('classifies HTTP %i without reading private errors or retrying', async (status, kind) => {
    const response = new Response('private-api-error', { status });
    const read = vi.spyOn(response, 'json');
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response);
    vi.stubGlobal('fetch', fetch);

    await expect(createMission(url, credentials, definition)).rejects.toEqual(new MissionCreateError(kind));
    expect(read).not.toHaveBeenCalled();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('expires only the failed connection session on authentication failure', async () => {
    const expired = vi.fn();
    window.addEventListener('commander:session-expired', expired);
    try {
      vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(null, { status: 401 })));
      await expect(createMission(url, credentials, definition)).rejects.toEqual(new MissionCreateError('authentication'));
      expect(expired).toHaveBeenCalledTimes(1);
      expect((expired.mock.calls[0][0] as CustomEvent).detail).toBe(url);
    } finally { window.removeEventListener('commander:session-expired', expired); }
  });

  it('treats wrong status, malformed or mismatched success and network failures as uncertain', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(Response.json(mission))
      .mockResolvedValueOnce(Response.json({ ...mission, name: 'another' }, { status: 201 }))
      .mockResolvedValueOnce(Response.json({ saved: true }, { status: 201 }))
      .mockResolvedValueOnce(new Response('private-proxy-response', { status: 201 }))
      .mockRejectedValueOnce(new TypeError('private-network-details'));
    vi.stubGlobal('fetch', fetch);

    for (let i = 0; i < 5; i++) {
      await expect(createMission(url, credentials, definition)).rejects.toEqual(new MissionCreateError('unknown'));
    }
    expect(fetch).toHaveBeenCalledTimes(5);
  });

  it.each(['send', 'body'] as const)('bounds the complete %s phase to thirty seconds without retrying', async phase => {
    vi.useFakeTimers();
    const response = Response.json(mission, { status: 201 });
    if (phase === 'body') vi.spyOn(response, 'json').mockImplementation(() => new Promise(() => {}));
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(() => phase === 'send' ? new Promise(() => {}) : Promise.resolve(response));
    vi.stubGlobal('fetch', fetch);
    const pending = expect(createMission(url, credentials, definition)).rejects.toEqual(new MissionCreateError('unknown'));

    await vi.advanceTimersByTimeAsync(30_000);

    await pending;
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('does not send pre-cancelled requests and treats cancellation after sending as uncertain', async () => {
    const controller = new AbortController();
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(() => new Promise(() => {}));
    vi.stubGlobal('fetch', fetch);
    const pending = createMission(url, credentials, definition, controller.signal);
    controller.abort();
    await expect(pending).rejects.toEqual(new MissionCreateError('unknown'));
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
    await expect(createMission(url, credentials, definition, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/api/client';
import {
  controlRuntimeSession, getRuntimeSession, listRuntimeSessions, SessionControlError, type RuntimeSession,
} from '../src/api/runtime-sessions';

const url = 'https://commander.example/team';
const credentials = { url, username: 'operator' };
const id = '5cde98e0-23d3-447d-ad59-2238b244f689';
const scopeID = 'ee870ebc-2230-49ed-8fc7-24429bcfd05d';
const requestID = 'f9dfb5e0-5ebf-4928-bcfa-1141d2889ba1';
const session: RuntimeSession = {
  id, scope_id: scopeID, owner_agent_id: 'browser-agent-0123abcd', kind: 'browser', label: 'Review browser',
  state: 'connected', disposition: 'active', busy: false, revision: 3,
  created_at: '2026-10-27T10:00:00Z', updated_at: '2026-10-27T10:01:00.123456789Z',
  connected_at: '2026-10-27T12:01:00+02:00', verified_at: '2026-10-27T10:01:00Z',
  endpoint: 'ws://runtime.example/session', hostname: 'worker', user: 'operator', peer: 'browser', reason: 'Observed connection',
};
const serverDetail = 'private server error that must not appear in the UI';

function respond(body: unknown, status = 200) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => Response.json(body, { status }));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function requestedPath(input: RequestInfo | URL): string {
  const request = new URL(String(input), window.location.origin);
  expect(request.origin).toBe(window.location.origin);
  expect(request.pathname).toBe('/ui/proxy');
  expect(request.searchParams.get('url')).toBe(url);
  return request.searchParams.get('path')!;
}

afterEach(() => vi.useRealTimers());

describe('session client', () => {
  it('lists filtered retained records through the authenticated same-origin proxy', async () => {
    const fetch = respond({ items: [{ ...session, unexpected: serverDetail }] });
    await expect(listRuntimeSessions(url, credentials, { scope_id: scopeID, owner_agent_id: session.owner_agent_id })).resolves.toEqual([session]);
    const [input, options] = fetch.mock.calls[0];
    const path = new URL(requestedPath(input), 'http://api.test');
    expect(path.pathname).toBe('/api/v1/sessions');
    expect([...path.searchParams]).toEqual([['scope_id', scopeID], ['owner_agent_id', session.owner_agent_id]]);
    expect(options).toMatchObject({ method: 'GET', credentials: 'same-origin', redirect: 'error', cache: 'no-store', headers: { 'X-Commander-UI': '1' } });
    expect(options?.headers).not.toHaveProperty('Authorization');
    expect(options?.body).toBeUndefined();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('reads ownerless reservations and retained release reasons without treating them as controllable', async () => {
    const reserved = { ...session, owner_agent_id: '', state: 'starting', disposition: 'reserved' };
    const fetch = respond(reserved);
    await expect(getRuntimeSession(url, credentials, id)).resolves.toEqual(reserved);
    expect(requestedPath(fetch.mock.calls[0][0])).toBe(`/api/v1/sessions/${id}`);
    await expect(controlRuntimeSession(url, credentials, reserved, 'close', requestID)).rejects.toEqual(new SessionControlError('invalid_input', requestID));
    expect(fetch).toHaveBeenCalledTimes(1);
    const released = { ...session, state: 'lost', disposition: 'released', release_reason: 'abandoned' };
    respond({ items: [released] });
    await expect(listRuntimeSessions(url, credentials)).resolves.toEqual([released]);
  });

  it.each([
    { id: 'not-a-uuid' }, { scope_id: id.toUpperCase() }, { owner_agent_id: 'browser-agent' },
    { state: 'ready' }, { disposition: 'unknown' }, { release_reason: 'expired' }, { kind: '' },
    { busy: 'false' }, { revision: 0 }, { revision: Number.MAX_SAFE_INTEGER + 1 },
    { updated_at: '2026-02-30T00:00:00Z' }, { created_at: '0001-01-01T00:00:00Z' },
    { connected_at: 'yesterday' }, { endpoint: 123 },
  ])('rejects malformed session metadata: %j', async patch => {
    respond({ ...session, ...patch });
    await expect(getRuntimeSession(url, credentials, id)).rejects.toEqual(new ApiError('invalid_response'));
  });

  it('rejects duplicate lists, filter mismatches, and foreign detail identities', async () => {
    respond({ items: [session, session] });
    await expect(listRuntimeSessions(url, credentials)).rejects.toEqual(new ApiError('invalid_response'));
    respond({ items: [session] });
    await expect(listRuntimeSessions(url, credentials, { scope_id: requestID })).rejects.toEqual(new ApiError('invalid_response'));
    await expect(listRuntimeSessions(url, credentials, { owner_agent_id: 'browser-agent-11111111' })).rejects.toEqual(new ApiError('invalid_response'));
    respond({ ...session, id: requestID });
    await expect(getRuntimeSession(url, credentials, id)).rejects.toEqual(new ApiError('invalid_response'));
    respond({ items: null });
    await expect(listRuntimeSessions(url, credentials)).rejects.toEqual(new ApiError('invalid_response'));
  });

  it('rejects invalid filters, references, controls and request IDs before making a request', async () => {
    const fetch = respond(session);
    await expect(listRuntimeSessions(url, credentials, { scope_id: 'invalid' })).rejects.toEqual(new ApiError('invalid_response'));
    await expect(listRuntimeSessions(url, credentials, { owner_agent_id: 'browser-agent' })).rejects.toEqual(new ApiError('invalid_response'));
    await expect(getRuntimeSession(url, credentials, '../sessions')).rejects.toEqual(new ApiError('invalid_response'));
    await expect(controlRuntimeSession(url, credentials, { ...session, scope_id: 'invalid' }, 'close', requestID)).rejects.toEqual(new SessionControlError('invalid_input', requestID));
    await expect(controlRuntimeSession(url, credentials, session, 'restart' as 'close', requestID)).rejects.toEqual(new SessionControlError('invalid_input', requestID));
    await expect(controlRuntimeSession(url, credentials, session, 'verify', 'invalid')).rejects.toEqual(new SessionControlError('invalid_input', 'invalid'));
    await expect(listRuntimeSessions(url, { ...credentials, url: 'https://other.example' })).rejects.toEqual(new ApiError('invalid_response'));
    expect(fetch).not.toHaveBeenCalled();
  });

  it.each(['inspect', 'verify', 'close', 'abandon'] as const)('submits one exact %s request without assuming its resulting state', async method => {
    const fetch = respond(session);
    await expect(controlRuntimeSession(url, credentials, session, method, requestID)).resolves.toEqual(session);
    expect(fetch).toHaveBeenCalledTimes(1);
    const [input, options] = fetch.mock.calls[0];
    expect(requestedPath(input)).toBe('/api/v1/sessions/control');
    expect(options).toMatchObject({
      method: 'POST', credentials: 'same-origin', redirect: 'error', cache: 'no-store',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-Commander-UI': '1' },
    });
    expect(JSON.parse(String(options?.body))).toEqual({ id, scope_id: scopeID, owner_agent_id: session.owner_agent_id, method, request_id: requestID });
  });

  it.each([
    [401, serverDetail, 'authentication'], [403, serverDetail, 'forbidden'], [404, 'session_not_found', 'not_found'],
    [400, 'invalid_request', 'invalid_input'], [409, serverDetail, 'conflict'], [422, serverDetail, 'conflict'],
    [503, 'sessions_unavailable', 'unavailable'], [503, 'session_control_uncertain', 'unknown'],
    [500, serverDetail, 'unknown'], [408, serverDetail, 'unknown'], [504, serverDetail, 'unknown'],
  ] as const)('classifies HTTP %s with a retained submitted request ID', async (status, code, kind) => {
    const fetch = respond({ error: code }, status);
    await expect(controlRuntimeSession(url, credentials, session, 'close', requestID)).rejects.toEqual(new SessionControlError(kind, requestID));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it.each([
    { ...session, id: requestID }, { ...session, scope_id: requestID },
    { ...session, owner_agent_id: 'browser-agent-11111111' }, { ...session, state: 'unknown' },
  ])('marks mismatched or malformed successful controls as uncertain', async body => {
    const fetch = respond(body);
    await expect(controlRuntimeSession(url, credentials, session, 'verify', requestID)).rejects.toEqual(new SessionControlError('unknown', requestID));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('requires exact 200 responses and sanitizes malformed bodies and network failures without retrying', async () => {
    let fetch = respond(session, 201);
    await expect(getRuntimeSession(url, credentials, id)).rejects.toEqual(new ApiError('invalid_response'));
    await expect(controlRuntimeSession(url, credentials, session, 'close', requestID)).rejects.toEqual(new SessionControlError('unknown', requestID));
    expect(fetch).toHaveBeenCalledTimes(2);
    fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(serverDetail, { status: 200 }));
    vi.stubGlobal('fetch', fetch);
    await expect(controlRuntimeSession(url, credentials, session, 'close', requestID)).rejects.toEqual(new SessionControlError('unknown', requestID));
    expect(fetch).toHaveBeenCalledTimes(1);
    fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new Error(serverDetail));
    vi.stubGlobal('fetch', fetch);
    await expect(listRuntimeSessions(url, credentials)).rejects.toEqual(new ApiError('unavailable'));
    await expect(controlRuntimeSession(url, credentials, session, 'close', requestID)).rejects.toEqual(new SessionControlError('unknown', requestID));
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('expires only the relevant connection on non-JSON 401 responses', async () => {
    const expired = vi.fn();
    window.addEventListener('commander:session-expired', expired);
    try {
      vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(serverDetail, { status: 401 })));
      await expect(controlRuntimeSession(url, credentials, session, 'verify', requestID)).rejects.toEqual(new SessionControlError('authentication', requestID));
      expect(expired).toHaveBeenCalledTimes(1);
      expect((expired.mock.calls[0][0] as CustomEvent).detail).toBe(url);
    } finally { window.removeEventListener('commander:session-expired', expired); }
  });

  it('never sends requests whose caller is already cancelled', async () => {
    const controller = new AbortController();
    controller.abort();
    const fetch = respond(session);
    await expect(getRuntimeSession(url, credentials, id, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    await expect(controlRuntimeSession(url, credentials, session, 'close', requestID, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetch).not.toHaveBeenCalled();
  });

  it.each(['read', 'control'] as const)('settles cancelled %s requests even if fetch ignores abort', async mode => {
    const controller = new AbortController();
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(() => new Promise(() => {}));
    vi.stubGlobal('fetch', fetch);
    const result = mode === 'control' ? controlRuntimeSession(url, credentials, session, 'close', requestID, controller.signal)
      : listRuntimeSessions(url, credentials, {}, controller.signal);
    const assertion = mode === 'control' ? expect(result).rejects.toEqual(new SessionControlError('unknown', requestID))
      : expect(result).rejects.toMatchObject({ name: 'AbortError' });
    controller.abort();
    await assertion;
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });

  it.each(['read', 'control'] as const)('bounds %s response-body processing and releases timers', async mode => {
    vi.useFakeTimers();
    const response = Response.json(session);
    vi.spyOn(response, 'json').mockImplementation(() => new Promise(() => {}));
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response);
    vi.stubGlobal('fetch', fetch);
    const result = mode === 'control' ? controlRuntimeSession(url, credentials, session, 'verify', requestID) : getRuntimeSession(url, credentials, id);
    const assertion = expect(result).rejects.toEqual(mode === 'control' ? new SessionControlError('unknown', requestID) : new ApiError('timeout'));
    await vi.advanceTimersByTimeAsync(mode === 'control' ? 30_000 : 5_000);
    await assertion;
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('ignores authentication responses after cancellation', async () => {
    let resolveResponse: (value: Response) => void = () => {};
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockImplementation(() => new Promise(resolve => { resolveResponse = resolve; })));
    const expired = vi.fn();
    window.addEventListener('commander:session-expired', expired);
    const controller = new AbortController();
    try {
      const result = expect(controlRuntimeSession(url, credentials, session, 'close', requestID, controller.signal)).rejects.toEqual(new SessionControlError('unknown', requestID));
      controller.abort();
      await result;
      resolveResponse(Response.json({ error: serverDetail }, { status: 401 }));
      await Promise.resolve();
      expect(expired).not.toHaveBeenCalled();
    } finally { window.removeEventListener('commander:session-expired', expired); }
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import { answerQuestion, cancelOperation, CommandError, startOperation } from '../src/api/operation-commands';
import type { Operation } from '../src/api/operation-types';

const url = 'https://commander.example/team';
const credentials = { url: url, username: 'operator' };

function requestedPath(input: RequestInfo | URL): string {
  const request = new URL(String(input), 'http://localhost');
  expect(request.origin).toBe('http://localhost');
  expect(request.pathname).toBe('/ui/proxy');
  expect(request.searchParams.get('url')).toBe(url);
  return request.searchParams.get('path')!;
}
const id = '5cde98e0-23d3-447d-ad59-2238b244f689';
const input = { operation_id: id, mission_name: 'review-website', input: { url: 'https://example.com' } };
const operation: Operation = {
  id, mission: { name: 'review-website', objective: 'Review a website', required_actions: null, starting_actions: null, input_schema: null, output_schema: null },
  input: input.input, status: 'planning', tasks: null, questions: null, events: null,
  created_at: '2026-10-05T12:00:00Z', updated_at: '2026-10-05T12:00:00Z',
};

function respond(body: unknown, status = 200) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(Response.json(body, { status }));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

afterEach(() => { vi.useRealTimers(); });

describe('operation commands', () => {
  it('posts start, cancel and answer as JSON through the session proxy without API credentials or redirects', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(Response.json(operation, { status: 201 }))
      .mockResolvedValueOnce(Response.json({ ...operation, status: 'cancelled' }))
      .mockResolvedValueOnce(Response.json(operation));
    vi.stubGlobal('fetch', fetch);

    expect(await startOperation(`${url}/`, credentials, input)).toEqual(operation);
    expect(await cancelOperation(url, credentials, id)).toEqual({ ...operation, status: 'cancelled' });
    expect(await answerQuestion(url, credentials, id, 'question-1', 'Continue with HTTP checks')).toEqual(operation);

    expect(fetch.mock.calls.map(([path]) => requestedPath(path))).toEqual([
      `/api/v1/operations`, `/api/v1/operations/${id}/cancel`,
      `/api/v1/operations/${id}/questions/question-1/answer`,
    ]);
    expect(fetch.mock.calls.map(([, options]) => JSON.parse(String(options?.body)))).toEqual([
      input, {}, { answer: 'Continue with HTTP checks' },
    ]);
    for (const [, options] of fetch.mock.calls) {
      expect(options).toMatchObject({
        method: 'POST', credentials: 'same-origin', redirect: 'error', cache: 'no-store',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-Commander-UI': '1' },
      });
      expect(new Headers(options?.headers).has('Authorization')).toBe(false);
    }
  });

  it('encodes every resource ID as one path segment', async () => {
    const operationID = 'operation/team ?#é';
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(Response.json({ ...operation, id: operationID }))
      .mockResolvedValueOnce(Response.json({ ...operation, id: operationID }));
    vi.stubGlobal('fetch', fetch);

    await cancelOperation(url, credentials, operationID);
    await answerQuestion(url, credentials, operationID, 'question/team ?#é', 'Continue');

    expect(fetch.mock.calls.map(([path]) => requestedPath(path))).toEqual([
      `/api/v1/operations/operation%2Fteam%20%3F%23%C3%A9/cancel`,
      `/api/v1/operations/operation%2Fteam%20%3F%23%C3%A9/questions/question%2Fteam%20%3F%23%C3%A9/answer`,
    ]);
  });

  it.each([
    [401, 'authentication', undefined], [403, 'forbidden', undefined],
    [404, 'not_found', 'mission_not_found'], [404, 'not_found', 'operation_not_found'],
    [409, 'conflict', 'operation_conflict'], [409, 'conflict', 'operation_finished'],
    [409, 'conflict', 'question_answered'], [409, 'conflict', 'question_expired'],
    [400, 'invalid_input', 'invalid_request'], [422, 'invalid_input', 'invalid_input'],
  ] as const)('recognizes HTTP %s / %s / %s without retrying', async (status, kind, code) => {
    const fetch = respond({ error: code ?? 'private server details' }, status);

    await expect(startOperation(url, credentials, input)).rejects.toMatchObject({ name: 'CommandError', kind, message: kind, code });
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('does not expose arbitrary server messages as error codes or text', async () => {
    const fetch = respond({ error: 'SECRET password=private-detail' }, 409);

    await expect(cancelOperation(url, credentials, id)).rejects.toEqual(new CommandError('conflict'));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('keeps an authentication error useful when the response is HTML instead of JSON', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response('<html>private proxy details</html>', { status: 401 }));
    vi.stubGlobal('fetch', fetch);

    await expect(startOperation(url, credentials, input)).rejects.toEqual(new CommandError('authentication'));
  });

  it.each([500, 503, 504])('treats HTTP %s as an unknown outcome even with a recognized body code', async status => {
    const fetch = respond({ error: 'operation_conflict' }, status);

    await expect(startOperation(url, credentials, input)).rejects.toEqual(new CommandError('unknown'));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it.each([
    { ...operation, id: 'another-operation' },
    { ...operation, tasks: [{ id: 'malformed-task' }] },
    { accepted: true },
  ])('treats a mismatched or malformed success as an unknown outcome', async result => {
    const fetch = respond(result);

    await expect(startOperation(url, credentials, input)).rejects.toEqual(new CommandError('unknown'));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('does not retry a network failure or leak its details', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new Error('private network details'));
    vi.stubGlobal('fetch', fetch);

    await expect(cancelOperation(url, credentials, id)).rejects.toEqual(new CommandError('unknown'));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('treats a non-JSON success as an unknown outcome', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response('private details', { status: 200 }));
    vi.stubGlobal('fetch', fetch);

    await expect(answerQuestion(url, credentials, id, 'question-1', 'Continue')).rejects.toEqual(new CommandError('unknown'));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('stops waiting after 30 seconds even when the transport does not settle after abort', async () => {
    vi.useFakeTimers();
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(() => new Promise(() => {}));
    vi.stubGlobal('fetch', fetch);
    const pending = startOperation(url, credentials, input);
    const assertion = expect(pending).rejects.toEqual(new CommandError('unknown'));

    await vi.advanceTimersByTimeAsync(30_000);

    await assertion;
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('includes stalled response-body processing in the 30-second command timeout', async () => {
    vi.useFakeTimers();
    const response = Response.json(operation);
    vi.spyOn(response, 'json').mockImplementation(() => new Promise(() => {}));
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response);
    vi.stubGlobal('fetch', fetch);
    const assertion = expect(startOperation(url, credentials, input)).rejects.toEqual(new CommandError('unknown'));

    await vi.advanceTimersByTimeAsync(30_000);

    await assertion;
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });

  it('does not send a command when the caller already aborted', async () => {
    const controller = new AbortController();
    controller.abort();
    const fetch = respond(operation);

    await expect(startOperation(url, credentials, input, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });

    expect(fetch).not.toHaveBeenCalled();
  });

  it('reports unknown outcome when the caller aborts a command already in flight', async () => {
    const controller = new AbortController();
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(() => new Promise(() => {}));
    vi.stubGlobal('fetch', fetch);
    const assertion = expect(startOperation(url, credentials, input, controller.signal)).rejects.toEqual(new CommandError('unknown'));

    controller.abort();

    await assertion;
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });

  it('rejects locally invalid fields and unserializable input before sending', async () => {
    const circular: Record<string, unknown> = {};
    circular.self = circular;
    const fetch = respond(operation);

    for (const request of [
      { ...input, operation_id: '' }, { ...input, mission_name: ' ' },
      { ...input, input: undefined }, { ...input, input: circular },
    ]) {
      await expect(startOperation(url, credentials, request)).rejects.toEqual(new CommandError('invalid_input'));
    }
    await expect(cancelOperation(url, credentials, '..')).rejects.toEqual(new CommandError('invalid_input'));
    await expect(answerQuestion(url, credentials, id, '\ud800', 'Continue')).rejects.toEqual(new CommandError('invalid_input'));
    await expect(answerQuestion(url, credentials, id, 'question-1', ' ')).rejects.toEqual(new CommandError('invalid_input'));

    expect(fetch).not.toHaveBeenCalled();
  });
});

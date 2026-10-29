import { afterEach, describe, expect, it, vi } from 'vitest';
import { answerQuestion, cancelOperation, completeOperation, CommandError, startOperation } from '../src/api/operation-commands';
import { respondJSON as respond } from './fakes/fetch';
import { completedOperation, credentials, id, input, operation, url } from './fixtures/operation-commands';

function requestedPath(input: RequestInfo | URL): string {
  const request = new URL(String(input), 'http://localhost');
  expect(request.origin).toBe('http://localhost');
  expect(request.pathname).toBe('/ui/proxy');
  expect(request.searchParams.get('url')).toBe(url);
  return request.searchParams.get('path')!;
}

afterEach(() => { vi.useRealTimers(); });

describe('operation commands', () => {
  it('retains raw completion output precision, number syntax and escapes', async () => {
    const output = String.raw`{ "identifier":9007199254740993, "ratio":0.10000000000000001, "magnitude":1e+100, "path":"\u0061\\path", "summary":"Reviewed" }`;
    const fetch = respond(completedOperation);

    await completeOperation(url, credentials, id, output);

    expect(requestedPath(fetch.mock.calls[0][0])).toBe(`/api/v1/operations/${id}/complete`);
    expect(fetch.mock.calls[0][1]?.body).toBe(`{"output":${output}}`);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('keeps JSON null distinct from missing completion output', async () => {
    const fetch = respond({ ...completedOperation, output: null });

    const saved = await completeOperation(url, credentials, id, 'null');

    expect(saved.output).toBeNull();
    expect(fetch.mock.calls[0][1]?.body).toBe('{"output":null}');
  });

  it('treats completion attributed to another channel as an unknown outcome', async () => {
    const fetch = respond({ ...completedOperation, completed_by: 'model' });

    await expect(completeOperation(url, credentials, id, '{}')).rejects.toEqual(new CommandError('unknown'));

    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('keeps an authentication error useful when the response is HTML instead of JSON', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response('<html>private proxy details</html>', { status: 401 }));
    vi.stubGlobal('fetch', fetch);

    await expect(startOperation(url, credentials, input)).rejects.toEqual(new CommandError('authentication'));
  });

  it('does not retry an uncertain server write or trust its error code', async () => {
    const fetch = respond({ error: 'operation_conflict' }, 503);

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
    await expect(completeOperation(url, credentials, '..', '{}')).rejects.toEqual(new CommandError('invalid_input'));
    for (const output of ['', ' ', '{"summary":', '{"output":{}},"completed_by":"operator"', 'undefined']) {
      await expect(completeOperation(url, credentials, id, output)).rejects.toEqual(new CommandError('invalid_input'));
    }

    expect(fetch).not.toHaveBeenCalled();
  });
});

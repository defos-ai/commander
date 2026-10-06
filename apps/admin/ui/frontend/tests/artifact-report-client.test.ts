import { afterEach, describe, expect, it, vi } from 'vitest';
import { FileTooLargeError, getArtifact, getArtifactFile, MAX_FILE_BYTES } from '../src/api/artifacts';
import { getOperationReport } from '../src/api/reports';
import type { Report } from '../src/api/reports';
import type { OperationArtifact } from '../src/api/operation-types';

const url = 'https://commander.example/team';
const credentials = { url, username: 'operator' };
const artifact: OperationArtifact = {
  id: 'artifact-1', name: 'Website screenshot', agent_id: 'agent-1', action_name: 'capture-page',
  created_at: '2026-10-05T12:00:00Z',
  files: [{ id: 'file-1', name: 'desktop.png', media_type: 'image/png', size: 4, sha256: 'abcd' }],
};
const report: Report = {
  id: 'report-1', operation_id: 'operation-1', mission: 'review-website',
  output: { summary: 'Review complete' }, created_at: '2026-10-05T12:00:00Z',
};

function proxyTarget(input: RequestInfo | URL) {
  const endpoint = new URL(String(input), window.location.origin);
  expect(endpoint.origin).toBe(window.location.origin);
  expect(endpoint.pathname).toBe('/ui/proxy');
  return Object.fromEntries(endpoint.searchParams);
}

function respond(body: unknown, status = 200) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(Response.json(body, { status }));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function blobText(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = reject;
    reader.readAsText(blob);
  });
}

afterEach(() => { vi.useRealTimers(); });

describe('artifact and report clients', () => {
  it('loads artifact metadata and the report belonging to the requested operation', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(Response.json(artifact))
      .mockResolvedValueOnce(Response.json(report));
    vi.stubGlobal('fetch', fetch);

    expect(await getArtifact(`${url}/`, credentials, artifact.id)).toEqual(artifact);
    expect(await getOperationReport(url, credentials, report.operation_id)).toEqual(report);

    expect(fetch.mock.calls.map(([path]) => proxyTarget(path))).toEqual([
      { url, path: '/api/v1/artifacts/artifact-1' }, { url, path: '/api/v1/operations/operation-1/report' },
    ]);
  });

  it('accepts fileless artifacts and reports whose output is arbitrary JSON', async () => {
    const fileless = { ...artifact, files: null };
    const plainOutput = { ...report, output: ['summary', 42, null] };
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(Response.json(fileless))
      .mockResolvedValueOnce(Response.json(plainOutput));
    vi.stubGlobal('fetch', fetch);

    expect(await getArtifact(url, credentials, artifact.id)).toEqual(fileless);
    expect(await getOperationReport(url, credentials, report.operation_id)).toEqual(plainOutput);
  });

  it.each([
    { ...artifact, id: 'different-artifact' },
    { ...artifact, files: [artifact.files![0], artifact.files![0]] },
    { ...artifact, files: [{ ...artifact.files![0], size: -1 }] },
  ])('rejects mismatched or malformed artifact metadata', async body => {
    respond(body);

    await expect(getArtifact(url, credentials, artifact.id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it.each([
    { ...report, operation_id: 'different-operation' },
    { ...report, created_at: 'not a date' },
    { id: report.id, operation_id: report.operation_id, mission: report.mission, created_at: report.created_at },
  ])('rejects reports with invalid ownership or fields', async body => {
    respond(body);

    await expect(getOperationReport(url, credentials, report.operation_id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it('encodes IDs as single path segments and never adds credentials to the file URL', async () => {
    const artifactID = 'artifact/team ?#é';
    const fileID = 'file/team ?#é';
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response('data', { headers: { 'Content-Type': 'text/plain' } }));
    vi.stubGlobal('fetch', fetch);

    const blob = await getArtifactFile(`${url}/`, credentials, artifactID, fileID);

    expect(await blobText(blob)).toBe('data');
    expect(blob.type).toBe('text/plain');
    expect(fetch).toHaveBeenCalledOnce();
    expect(proxyTarget(fetch.mock.calls[0][0])).toEqual({ url, path: '/api/v1/artifacts/artifact%2Fteam%20%3F%23%C3%A9/files/file%2Fteam%20%3F%23%C3%A9' });
    expect(fetch.mock.calls[0][1]).toMatchObject({ method: 'GET', credentials: 'same-origin', redirect: 'error', cache: 'no-store',
      headers: { Accept: '*/*', 'X-Commander-UI': '1' } });
    expect(new Headers(fetch.mock.calls[0][1]?.headers).has('Authorization')).toBe(false);
  });

  it('rejects an oversized Content-Length before reading the body and caps caller limits at 100 MiB', async () => {
    const response = new Response('small', { headers: { 'Content-Length': String(MAX_FILE_BYTES + 1) } });
    const read = vi.spyOn(response.body!, 'getReader');
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response);
    vi.stubGlobal('fetch', fetch);

    await expect(getArtifactFile(url, credentials, artifact.id, 'file-1', undefined, MAX_FILE_BYTES * 2))
      .rejects.toMatchObject({ name: 'FileTooLargeError', maxBytes: MAX_FILE_BYTES });

    expect(read).not.toHaveBeenCalled();
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });

  it('enforces the preview byte limit while reading chunks even when Content-Length is incorrect', async () => {
    const cancel = vi.fn();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) { controller.enqueue(new Uint8Array([1, 2, 3])); controller.enqueue(new Uint8Array([4, 5, 6])); }, cancel,
    });
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(stream, { headers: { 'Content-Length': '1' } }));
    vi.stubGlobal('fetch', fetch);

    await expect(getArtifactFile(url, credentials, artifact.id, 'file-1', undefined, 5)).rejects.toEqual(new FileTooLargeError(5));

    expect(cancel).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('accepts a chunked file exactly at the selected limit without relying on Content-Length', async () => {
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('ab'));
        controller.enqueue(new TextEncoder().encode('cde'));
        controller.close();
      },
    });
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(stream));
    vi.stubGlobal('fetch', fetch);

    const blob = await getArtifactFile(url, credentials, artifact.id, 'file-1', undefined, 5);

    expect(blob.size).toBe(5);
    expect(await blobText(blob)).toBe('abcde');
    expect(blob.type).toBe('application/octet-stream');
  });

  it.each([[401, 'authentication'], [403, 'forbidden'], [404, 'not_found'], [500, 'unavailable']] as const)(
    'sanitizes HTTP %s for file requests without consuming the error body', async (status, kind) => {
      const response = new Response('private server details', { status });
      const read = vi.spyOn(response.body!, 'getReader');
      vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockResolvedValue(response));

      await expect(getArtifactFile(url, credentials, artifact.id, 'file-1')).rejects.toMatchObject({ kind, message: kind });

      expect(read).not.toHaveBeenCalled();
    },
  );

  it('times out an unresponsive file transport after 30 seconds without retrying', async () => {
    vi.useFakeTimers();
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(() => new Promise(() => {}));
    vi.stubGlobal('fetch', fetch);
    const assertion = expect(getArtifactFile(url, credentials, artifact.id, 'file-1')).rejects.toMatchObject({ kind: 'timeout' });

    await vi.advanceTimersByTimeAsync(30_000);

    await assertion;
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('includes stalled body reads in the download timeout and cancels the stream', async () => {
    vi.useFakeTimers();
    const cancel = vi.fn();
    const stream = new ReadableStream<Uint8Array>({ cancel });
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(stream)));
    const assertion = expect(getArtifactFile(url, credentials, artifact.id, 'file-1')).rejects.toMatchObject({ kind: 'timeout' });

    await vi.advanceTimersByTimeAsync(30_000);

    await assertion;
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('cancels an active body read when the caller aborts', async () => {
    const controller = new AbortController();
    const cancel = vi.fn();
    const stream = new ReadableStream<Uint8Array>({ cancel });
    const response = new Response(stream);
    const read = vi.spyOn(response.body!, 'getReader');
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>().mockResolvedValue(response));
    const assertion = expect(getArtifactFile(url, credentials, artifact.id, 'file-1', controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    await Promise.resolve();
    expect(read).toHaveBeenCalledTimes(1);

    controller.abort();

    await assertion;
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('does not read a late response after the caller aborts', async () => {
    const controller = new AbortController();
    let complete!: (response: Response) => void;
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(() => new Promise(resolve => { complete = resolve; }));
    vi.stubGlobal('fetch', fetch);
    const assertion = expect(getArtifactFile(url, credentials, artifact.id, 'file-1', controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    const response = new Response('late data');
    const read = vi.spyOn(response.body!, 'getReader');

    controller.abort();
    await assertion;
    complete(response);
    await Promise.resolve();

    expect(read).not.toHaveBeenCalled();
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });

  it('rejects invalid limits, path identifiers and pre-aborted requests before fetching', async () => {
    const fetch = respond(artifact);
    const controller = new AbortController();
    controller.abort();

    await expect(getArtifactFile(url, credentials, artifact.id, 'file-1', controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    await expect(getArtifactFile(url, credentials, artifact.id, '..')).rejects.toMatchObject({ kind: 'invalid_response' });
    await expect(getArtifactFile(url, credentials, artifact.id, 'file-1', undefined, 0)).rejects.toMatchObject({ kind: 'invalid_response' });
    await expect(getArtifact(url, credentials, '\ud800')).rejects.toMatchObject({ kind: 'invalid_response' });
    await expect(getOperationReport(url, credentials, '')).rejects.toMatchObject({ kind: 'invalid_response' });

    expect(fetch).not.toHaveBeenCalled();
  });
});

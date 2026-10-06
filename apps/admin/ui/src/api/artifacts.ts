import { basicAuthorization, normalizeURL } from '../connections/client';
import type { Credentials } from '../connections/types';
import { ApiError, getJSON } from './client';
import type { OperationArtifact } from './operation-types';
import { isArtifact } from './operations';
import { encodeIdentifier } from './validation';

export const MAX_FILE_BYTES = 100 * 1024 * 1024;

export class FileTooLargeError extends Error {
  constructor(readonly maxBytes = MAX_FILE_BYTES) {
    super('File exceeds the download limit');
    this.name = 'FileTooLargeError';
  }
}

export async function getArtifact(url: string, credentials: Credentials, artifactID: string, signal?: AbortSignal): Promise<OperationArtifact> {
  const body = await getJSON(url, `/api/v1/artifacts/${encodeIdentifier(artifactID)}`, credentials, signal);
  if (!isArtifact(body) || body.id !== artifactID || body.files && new Set(body.files.map(file => file.id)).size !== body.files.length) {
    throw new ApiError('invalid_response');
  }
  return body;
}

export async function getArtifactFile(url: string, credentials: Credentials, artifactID: string, fileID: string, signal?: AbortSignal, maxBytes = MAX_FILE_BYTES): Promise<Blob> {
  if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
  if (!Number.isSafeInteger(maxBytes) || maxBytes <= 0) throw new ApiError('invalid_response');
  const limit = Math.min(maxBytes, MAX_FILE_BYTES);
  const endpoint = `${normalizeURL(url)}/api/v1/artifacts/${encodeIdentifier(artifactID)}/files/${encodeIdentifier(fileID)}`;
  const authorization = basicAuthorization(credentials);
  const controller = new AbortController();
  const abort = () => controller.abort();
  let timedOut = false;
  let reader: ReadableStreamDefaultReader<Uint8Array<ArrayBuffer>> | undefined;
  const aborted = new Promise<never>((_, reject) => {
    controller.signal.addEventListener('abort', () => reject(new DOMException('Request aborted', 'AbortError')), { once: true });
  });
  signal?.addEventListener('abort', abort, { once: true });
  const timeout = setTimeout(() => { timedOut = true; controller.abort(); }, 30_000);

  async function download() {
    const response = await fetch(endpoint, {
      method: 'GET', headers: { Accept: '*/*', Authorization: authorization },
      credentials: 'omit', redirect: 'error', cache: 'no-store', signal: controller.signal,
    });
    controller.signal.throwIfAborted();
    if (!response.ok) {
      const kind = response.status === 401 ? 'authentication'
        : response.status === 403 ? 'forbidden'
          : response.status === 404 ? 'not_found'
            : response.status === 408 || response.status === 504 ? 'timeout' : 'unavailable';
      throw new ApiError(kind);
    }
    const length = response.headers.get('Content-Length');
    if (length !== null && Number(length) > limit) throw new FileTooLargeError(limit);
    const chunks: BlobPart[] = [];
    let size = 0;
    if (response.body) {
      reader = response.body.getReader();
      for (;;) {
        const { done, value } = await reader.read();
        controller.signal.throwIfAborted();
        if (done) break;
        size += value.byteLength;
        if (size > limit) throw new FileTooLargeError(limit);
        chunks.push(value);
      }
    }
    return new Blob(chunks, { type: response.headers.get('Content-Type') || 'application/octet-stream' });
  }

  try {
    return await Promise.race([download(), aborted]);
  } catch (error) {
    if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
    if (timedOut) throw new ApiError('timeout');
    if (error instanceof FileTooLargeError || error instanceof ApiError) throw error;
    throw new ApiError('unavailable');
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener('abort', abort);
    controller.abort();
    void reader?.cancel().catch(() => {});
  }
}

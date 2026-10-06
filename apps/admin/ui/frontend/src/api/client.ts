import { expireSession, proxyURL } from '../connections/client';
import type { Session } from '../connections/types';

export type ApiErrorKind = 'authentication' | 'forbidden' | 'not_found' | 'unavailable' | 'invalid_response' | 'timeout';

export class ApiError extends Error {
  constructor(readonly kind: ApiErrorKind) {
    super(kind);
    this.name = 'ApiError';
  }
}

export async function getJSON(url: string, path: string, credentials: Session, signal?: AbortSignal): Promise<unknown> {
  if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
  const endpoint = proxyURL(url, path, credentials);
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true });
  let timedOut = false;
  const timeout = setTimeout(() => { timedOut = true; controller.abort(); }, 5_000);

  try {
    const response = await fetch(endpoint, {
      method: 'GET',
      headers: { Accept: 'application/json', 'X-Commander-UI': '1' },
      credentials: 'same-origin',
      redirect: 'error',
      cache: 'no-store',
      signal: controller.signal,
    });
    if (response.status === 401 && !controller.signal.aborted) expireSession(url);
    if (!response.ok) {
      const kind = response.status === 401 ? 'authentication'
        : response.status === 403 ? 'forbidden'
          : response.status === 404 ? 'not_found'
            : response.status === 408 || response.status === 504 ? 'timeout' : 'unavailable';
      throw new ApiError(kind);
    }
    try {
      const body: unknown = await response.json();
      controller.signal.throwIfAborted();
      return body;
    } catch {
      throw new ApiError('invalid_response');
    }
  } catch (error) {
    if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
    if (timedOut) throw new ApiError('timeout');
    if (error instanceof ApiError) throw error;
    throw new ApiError('unavailable');
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener('abort', abort);
  }
}

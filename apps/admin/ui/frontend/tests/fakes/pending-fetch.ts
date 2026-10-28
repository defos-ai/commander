import { vi } from 'vitest';

export function pendingFetch() {
  const fetch = vi.fn<typeof globalThis.fetch>((_url, options) => new Promise((_resolve, reject) => {
    options?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
  }));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

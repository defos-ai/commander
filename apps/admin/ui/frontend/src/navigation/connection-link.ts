import { normalizeURL } from '../connections/client';

export type ConnectionPrefill = { url: string; username: string };

export function parseConnectionPrefill(query: string): ConnectionPrefill | null {
  try {
    // Reject malformed escapes rather than accepting URLSearchParams' replacement characters.
    decodeURIComponent(query);
    const params = new URLSearchParams(query);
    if ([...params.keys()].some(key => key !== 'api' && key !== 'username')
      || params.getAll('api').length !== 1 || params.getAll('username').length !== 1) return null;
    const api = params.get('api')!;
    const username = params.get('username')!;
    if (!api || api.length > 2048 || /[\\\u0000-\u0020\u007f-\u009f]/u.test(api)
      || !username.trim() || username.length > 256 || /[:\u0000-\u001f\u007f-\u009f]/u.test(username)) return null;
    return { url: normalizeURL(api), username };
  } catch {
    return null;
  }
}

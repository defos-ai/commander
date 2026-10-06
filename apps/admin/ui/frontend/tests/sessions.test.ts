import { describe, expect, it, vi } from 'vitest';
import { createSession, deleteSession, listSessions, SessionError } from '../src/connections/sessions';

const url = 'https://api.example.test';

describe('UI server sessions', () => {
  it('submits credentials once to the same-origin server and returns only public session fields', async () => {
    const fetch = vi.fn().mockResolvedValue(Response.json({ url, username: 'operator', password: 'must not escape' }));
    vi.stubGlobal('fetch', fetch);
    const session = await createSession(`${url}/`, { username: 'operator', password: ' exact secret ' });
    expect(session).toEqual({ url, username: 'operator' });
    expect(fetch).toHaveBeenCalledOnce();
    const [path, options] = fetch.mock.calls[0];
    expect(path).toBe('/ui/connections');
    expect(options).toMatchObject({ method: 'POST', credentials: 'same-origin', redirect: 'error', cache: 'no-store' });
    expect(JSON.parse(options.body)).toEqual({ url, username: 'operator', password: ' exact secret ' });
    expect(new Headers(options.headers).get('X-Commander-UI')).toBe('1');
    expect(new Headers(options.headers).has('Authorization')).toBe(false);
  });

  it('restores multiple sessions and deletes only the selected API without resending credentials', async () => {
    const other = 'https://other.example.test';
    const fetch = vi.fn()
      .mockResolvedValueOnce(Response.json({ items: [{ url, username: 'one' }, { url: other, username: 'two' }] }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetch);
    expect(await listSessions()).toEqual([{ url, username: 'one' }, { url: other, username: 'two' }]);
    await deleteSession(url);
    const [path, options] = fetch.mock.calls[1];
    expect(new URL(path, 'http://ui.test').searchParams.get('url')).toBe(url);
    expect(options).toMatchObject({ method: 'DELETE', credentials: 'same-origin' });
    expect(options.body).toBeUndefined();
  });

  it('rejects a login response for another target', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ url: 'https://other.example.test', username: 'operator' })));
    await expect(createSession(url, { username: 'operator', password: 'secret' })).rejects.toEqual(new SessionError('invalid_response'));
  });

  it.each([[401, 'authentication'], [403, 'forbidden'], [502, 'unavailable']])('classifies HTTP %s without exposing its response body', async (status, kind) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: 'upstream secret' }, { status: status as number })));
    await expect(listSessions()).rejects.toMatchObject({ message: kind, kind });
  });
});

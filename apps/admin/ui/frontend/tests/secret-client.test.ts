import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createSecret, deleteSecret, getSecret, listSecretRequests, listSecrets, replaceSecret, SecretError,
  type PendingSecretRequest, type SecretMetadata,
} from '../src/api/secrets';

const url = 'https://commander.example/team';
const credentials = { url, username: 'operator' };
const key = { agent_type: 'builder-agent', name: 'GITHUB_TOKEN' };
const id = '5cde98e0-23d3-447d-ad59-2238b244f689';
const metadata: SecretMetadata = {
  ...key, id, version: 1, status: 'available', created_at: '2026-10-17T10:00:00Z',
  updated_at: '2026-10-17T10:00:00.123456789Z', expires_at: '2027-10-17T12:00:00+02:00',
};
const pending: PendingSecretRequest = {
  ...key, id, agent_id: 'builder-agent-0123abcd', requested_at: '2026-10-17T10:00:00Z',
  expires_at: '2026-10-17T10:01:00Z', status: 'needs_value',
};
const secretValue = 'test-private-value-not-for-display';
const createInput = { ...key, value: secretValue };
const replacement = { id, version: 1, value: secretValue };
const itemPath = '/api/v1/secrets/builder-agent/GITHUB_TOKEN';

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
  expect(String(input)).not.toContain(secretValue);
  return request.searchParams.get('path')!;
}

afterEach(() => vi.useRealTimers());

describe('secret management client', () => {
  it('uses the authenticated same-origin proxy and safe metadata for listing, reading and writing', async () => {
    const updated = { ...metadata, version: 2 };
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(Response.json({ items: [metadata] }))
      .mockResolvedValueOnce(Response.json({ items: [pending] }))
      .mockResolvedValueOnce(Response.json(metadata))
      .mockResolvedValueOnce(Response.json(metadata, { status: 201 }))
      .mockResolvedValueOnce(Response.json(updated))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetch);
    const stored = vi.spyOn(Storage.prototype, 'setItem');

    expect(await listSecrets(`${url}/`, credentials, key.agent_type)).toEqual([metadata]);
    expect(await listSecretRequests(url, credentials)).toEqual([pending]);
    expect(await getSecret(url, credentials, key)).toEqual(metadata);
    expect(await createSecret(url, credentials, createInput)).toEqual(metadata);
    expect(await replaceSecret(url, credentials, key, replacement)).toEqual(updated);
    expect(await deleteSecret(url, credentials, updated)).toBeUndefined();

    expect(fetch.mock.calls.map(([path]) => requestedPath(path))).toEqual([
      '/api/v1/secrets?agent_type=builder-agent', '/api/v1/secrets/requests', itemPath,
      '/api/v1/secrets', itemPath, `${itemPath}?id=${id}&version=2`,
    ]);
    expect(fetch.mock.calls.map(([, options]) => options?.method)).toEqual(['GET', 'GET', 'GET', 'POST', 'PUT', 'DELETE']);
    expect(fetch.mock.calls.map(([, options]) => options?.body ? JSON.parse(String(options.body)) : undefined))
      .toEqual([undefined, undefined, undefined, createInput, replacement, undefined]);
    for (const [, options] of fetch.mock.calls) {
      expect(options).toMatchObject({ credentials: 'same-origin', redirect: 'error', cache: 'no-store' });
      const headers = new Headers(options?.headers);
      expect(headers.get('Accept')).toBe('application/json');
      expect(headers.get('X-Commander-UI')).toBe('1');
      expect(headers.has('Authorization')).toBe(false);
    }
    expect(stored).not.toHaveBeenCalled();
  });

  it('projects only public fields, ignoring encrypted envelopes or accidentally returned plaintext', async () => {
    const extra = { value: secretValue, ciphertext: secretValue, nonce: secretValue, key_id: secretValue };
    respond({ items: [{ ...metadata, ...extra }] });
    expect(await listSecrets(url, credentials, key.agent_type)).toEqual([metadata]);
    respond({ items: [{ ...pending, ...extra, delivery: extra }] });
    expect(await listSecretRequests(url, credentials)).toEqual([pending]);
    respond({ ...metadata, ...extra }, 201);
    expect(await createSecret(url, credentials, createInput)).toEqual(metadata);
  });

  it('accepts empty inventories, expired metadata and each pending delivery stage', async () => {
    respond({ items: [] });
    expect(await listSecrets(url, credentials, key.agent_type)).toEqual([]);
    expect(await listSecretRequests(url, credentials)).toEqual([]);
    respond({ ...metadata, status: 'expired', expires_at: undefined });
    expect(await getSecret(url, credentials, key)).toEqual({ ...metadata, status: 'expired', expires_at: undefined });
    for (const status of ['needs_value', 'awaiting_delivery', 'awaiting_installation'] as const) {
      const request = { ...pending, status, operation_id: id };
      respond({ items: [request] });
      expect(await listSecretRequests(url, credentials)).toEqual([request]);
    }
  });

  it.each([
    { id: 'not-a-uuid' }, { id: id.toUpperCase() }, { agent_type: 'Builder' }, { agent_type: 'a'.repeat(64) },
    { name: '../GITHUB_TOKEN' }, { version: 0 }, { version: 1.5 }, { version: Number.MAX_SAFE_INTEGER + 1 },
    { status: 'pending' }, { created_at: 'yesterday' }, { updated_at: '2026-02-30T10:00:00Z' },
    { expires_at: null }, { expires_at: '2026-10-17' }, { updated_at: '2026-10-17T25:00:00Z' },
  ])('rejects malformed secret metadata: %j', async patch => {
    respond({ items: [{ ...metadata, ...patch }] });
    await expect(listSecrets(url, credentials, key.agent_type)).rejects.toEqual(new SecretError('invalid_response'));
  });

  it.each([
    { id: 'bad' }, { agent_id: 'builder-agent-ABCDef12' }, { agent_id: 'missing-role-0123abcd' },
    { operation_id: '' }, { operation_id: 'bad' }, { status: 'fulfilled' }, { requested_at: null },
    { name: ' ' }, { agent_type: 'invalid--type' }, { expires_at: '2026-10-17T10:00:00' },
  ])('rejects malformed pending requests: %j', async patch => {
    respond({ items: [{ ...pending, ...patch }] });
    await expect(listSecretRequests(url, credentials)).rejects.toEqual(new SecretError('invalid_response'));
  });

  it('rejects mismatched identities, duplicate inventory entries and malformed envelopes', async () => {
    for (const body of [{}, [], { items: null }, { items: [metadata, metadata] }, { items: [{ ...metadata, agent_type: 'web-agent' }] }]) {
      respond(body);
      await expect(listSecrets(url, credentials, key.agent_type)).rejects.toEqual(new SecretError('invalid_response'));
    }
    respond({ ...metadata, name: 'OTHER_TOKEN' });
    await expect(getSecret(url, credentials, key)).rejects.toEqual(new SecretError('invalid_response'));
    respond({ items: [pending, pending] });
    await expect(listSecretRequests(url, credentials)).rejects.toEqual(new SecretError('invalid_response'));
  });

  it('validates local inputs and session identity before sending values', async () => {
    const fetch = respond(metadata, 201);
    await expect(listSecrets(url, credentials, '')).rejects.toEqual(new SecretError('invalid_input'));
    await expect(getSecret(url, credentials, { ...key, name: '../escape' })).rejects.toEqual(new SecretError('invalid_input'));
    await expect(createSecret(url, credentials, { ...createInput, expires_at: 'invalid' })).rejects.toEqual(new SecretError('invalid_input'));
    await expect(replaceSecret(url, credentials, key, { ...replacement, version: Number.MAX_SAFE_INTEGER })).rejects.toEqual(new SecretError('invalid_input'));
    await expect(deleteSecret(url, credentials, { ...metadata, version: Number.MAX_SAFE_INTEGER + 1 })).rejects.toEqual(new SecretError('invalid_input'));
    await expect(createSecret('https://other.example', credentials, createInput)).rejects.toEqual(new SecretError('invalid_input'));
    expect(fetch).not.toHaveBeenCalled();
  });

  it.each([
    [400, 'invalid_input'], [401, 'authentication'], [403, 'forbidden'], [404, 'not_found'], [409, 'conflict'],
  ] as const)('reports HTTP %i safely without repeating a write', async (status, kind) => {
    const fetch = respond({ error: secretValue, message: secretValue }, status);
    await expect(createSecret(url, credentials, createInput)).rejects.toEqual(new SecretError(kind));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('recognizes disabled secret management only from its explicit server code', async () => {
    respond({ error: 'secrets_unavailable' }, 503);
    await expect(listSecretRequests(url, credentials)).rejects.toEqual(new SecretError('disabled'));
    await expect(createSecret(url, credentials, createInput)).rejects.toEqual(new SecretError('disabled'));
    respond({ error: 'internal_error' }, 503);
    await expect(listSecretRequests(url, credentials)).rejects.toEqual(new SecretError('unavailable'));
    await expect(createSecret(url, credentials, createInput)).rejects.toEqual(new SecretError('unknown'));
  });

  it.each([408, 500, 503, 504])('treats HTTP %i writes as unknown outcomes, even with a misleading body', async status => {
    const fetch = respond({ error: 'secret_conflict' }, status);
    await expect(createSecret(url, credentials, createInput)).rejects.toEqual(new SecretError('unknown'));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('does not treat uncertain writes as safe retries', async () => {
    const fetch = respond({ error: 'secret_write_uncertain' }, 503);
    await expect(deleteSecret(url, credentials, metadata)).rejects.toEqual(new SecretError('unknown'));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it.each([
    { ...metadata, name: 'DIFFERENT_TOKEN' }, { ...metadata, version: 2 }, { saved: true },
  ])('treats malformed or mismatched creation success as unknown', async body => {
    const fetch = respond(body, 201);
    await expect(createSecret(url, credentials, createInput)).rejects.toEqual(new SecretError('unknown'));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('requires replacement identity/version and exact success status', async () => {
    respond({ ...metadata, version: 3 });
    await expect(replaceSecret(url, credentials, key, replacement)).rejects.toEqual(new SecretError('unknown'));
    respond({ ...metadata, id: 'ad382fcb-39c7-4f12-b7fb-0dcad021e151', version: 2 });
    await expect(replaceSecret(url, credentials, key, replacement)).rejects.toEqual(new SecretError('unknown'));
    respond(metadata);
    await expect(createSecret(url, credentials, createInput)).rejects.toEqual(new SecretError('unknown'));
    await expect(deleteSecret(url, credentials, metadata)).rejects.toEqual(new SecretError('unknown'));
  });

  it('does not leak fetch failures or automatically retry requests', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new Error(secretValue));
    vi.stubGlobal('fetch', fetch);
    await expect(getSecret(url, credentials, key)).rejects.toEqual(new SecretError('unavailable'));
    await expect(createSecret(url, credentials, createInput)).rejects.toEqual(new SecretError('unknown'));
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('sanitizes non-JSON responses without losing HTTP authentication status', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(new Response(secretValue, { status: 401 }))
      .mockResolvedValueOnce(new Response(secretValue, { status: 200 }))
      .mockResolvedValueOnce(new Response(secretValue, { status: 201 }));
    vi.stubGlobal('fetch', fetch);
    await expect(getSecret(url, credentials, key)).rejects.toEqual(new SecretError('authentication'));
    await expect(getSecret(url, credentials, key)).rejects.toEqual(new SecretError('invalid_response'));
    await expect(createSecret(url, credentials, createInput)).rejects.toEqual(new SecretError('unknown'));
  });

  it('expires only the affected active connection session on HTTP 401', async () => {
    const expired = vi.fn();
    window.addEventListener('commander:session-expired', expired);
    try {
      respond({ error: secretValue }, 401);
      await expect(listSecretRequests(url, credentials)).rejects.toEqual(new SecretError('authentication'));
      expect(expired).toHaveBeenCalledTimes(1);
      expect((expired.mock.calls[0][0] as CustomEvent).detail).toBe(url);
    } finally { window.removeEventListener('commander:session-expired', expired); }
  });

  it('never sends already-cancelled reads or writes', async () => {
    const controller = new AbortController();
    controller.abort();
    const fetch = respond(metadata, 201);
    await expect(getSecret(url, credentials, key, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    await expect(createSecret(url, credentials, createInput, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetch).not.toHaveBeenCalled();
  });

  it.each(['read', 'write'] as const)('settles cancelled %s requests even when fetch ignores abort', async mode => {
    const controller = new AbortController();
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(() => new Promise(() => {}));
    vi.stubGlobal('fetch', fetch);
    const result = mode === 'write' ? createSecret(url, credentials, createInput, controller.signal)
      : listSecretRequests(url, credentials, controller.signal);
    const assertion = mode === 'write' ? expect(result).rejects.toEqual(new SecretError('unknown'))
      : expect(result).rejects.toMatchObject({ name: 'AbortError' });
    controller.abort();
    await assertion;
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });

  it.each(['read', 'write'] as const)('bounds %s response-body processing and clears its timeout', async mode => {
    vi.useFakeTimers();
    const response = Response.json(mode === 'write' ? metadata : { items: [] }, { status: mode === 'write' ? 201 : 200 });
    vi.spyOn(response, 'json').mockImplementation(() => new Promise(() => {}));
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response);
    vi.stubGlobal('fetch', fetch);
    const result = mode === 'write' ? createSecret(url, credentials, createInput) : listSecretRequests(url, credentials);
    const assertion = expect(result).rejects.toEqual(new SecretError(mode === 'write' ? 'unknown' : 'timeout'));
    await vi.advanceTimersByTimeAsync(mode === 'write' ? 30_000 : 5_000);
    await assertion;
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('ignores a late authentication response from a cancelled request', async () => {
    let resolveResponse: (value: Response) => void = () => {};
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(() => new Promise(resolve => { resolveResponse = resolve; }));
    vi.stubGlobal('fetch', fetch);
    const controller = new AbortController();
    const expired = vi.fn();
    window.addEventListener('commander:session-expired', expired);
    try {
      const result = expect(listSecretRequests(url, credentials, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
      controller.abort();
      await result;
      resolveResponse(Response.json({ error: secretValue }, { status: 401 }));
      await Promise.resolve();
      expect(expired).not.toHaveBeenCalled();
    } finally { window.removeEventListener('commander:session-expired', expired); }
  });
});

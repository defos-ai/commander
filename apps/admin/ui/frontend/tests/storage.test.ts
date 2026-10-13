import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CONNECTIONS_STORAGE_KEY, loadConnections, saveConnections } from '../src/connections/storage';

const connection = { id: 'local', name: 'Local Commander', url: 'http://localhost:4274' };

beforeEach(() => localStorage.clear());

describe('connection metadata storage', () => {
  it('persists and reloads only metadata, even if a caller passes credentials or other state', () => {
    const withSecrets = { ...connection, username: 'alice', password: 'secret', credentials: { username: 'alice', password: 'secret' }, status: 'ready' };

    expect(saveConnections([withSecrets])).toBe(true);

    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).toBe(JSON.stringify([connection]));
    expect(loadConnections()).toEqual([connection]);
  });

  it('normalizes saved URLs and discards extra properties while loading', () => {
    localStorage.setItem(CONNECTIONS_STORAGE_KEY, JSON.stringify([{ ...connection, url: ' HTTPS://EXAMPLE.COM:443/commander/ ', password: 'old-secret' }]));

    expect(loadConnections()).toEqual([{ ...connection, url: 'https://example.com/commander' }]);
  });

  it.each([
    'https://operator:secret@example.com',
    'https://example.com?token=secret',
    'https://example.com#token=secret',
  ])('rejects credential-bearing URLs before saving: %s', url => {
    expect(saveConnections([connection])).toBe(true);

    expect(saveConnections([{ ...connection, url }])).toBe(false);

    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).toBe(JSON.stringify([connection]));
  });

  it('returns an empty list for missing storage', () => {
    expect(loadConnections()).toEqual([]);
  });

  it.each([
    'not JSON',
    '{}',
    '[null]',
    JSON.stringify([{ ...connection, id: '' }]),
    JSON.stringify([{ ...connection, name: ' ' }]),
    JSON.stringify([{ ...connection, url: 'https://user:secret@example.com' }]),
    JSON.stringify([connection, connection]),
    JSON.stringify([connection, { id: 'incomplete' }]),
  ])('ignores corrupt or invalid metadata: %s', value => {
    localStorage.setItem(CONNECTIONS_STORAGE_KEY, value);

    expect(loadConnections()).toEqual([]);
  });

  it('tolerates blocked reads and reports blocked writes without throwing', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new DOMException('Blocked', 'SecurityError'); });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('Quota exceeded', 'QuotaExceededError'); });

    expect(loadConnections()).toEqual([]);
    expect(saveConnections([connection])).toBe(false);
  });
});

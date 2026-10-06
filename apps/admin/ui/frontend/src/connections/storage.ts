import { normalizeURL } from './client';
import type { Connection } from './types';

export const CONNECTIONS_STORAGE_KEY = 'commander.connections.v1';

export function loadConnections(): Connection[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(CONNECTIONS_STORAGE_KEY) ?? '[]');
    if (!Array.isArray(value)) return [];

    const ids = new Set<string>();
    return value.map((entry: unknown) => {
      if (
        typeof entry !== 'object' || entry === null ||
        !('id' in entry) || typeof entry.id !== 'string' || !entry.id.trim() ||
        !('name' in entry) || typeof entry.name !== 'string' || !entry.name.trim() ||
        !('url' in entry) || typeof entry.url !== 'string' || !entry.url.trim() ||
        ids.has(entry.id)
      ) {
        throw new Error('Invalid connection metadata');
      }
      ids.add(entry.id);
      return { id: entry.id, name: entry.name, url: normalizeURL(entry.url) };
    });
  } catch {
    return [];
  }
}

export function saveConnections(connections: Connection[]): boolean {
  try {
    const metadata = connections.map(({ id, name, url }) => ({ id, name, url }));
    localStorage.setItem(CONNECTIONS_STORAGE_KEY, JSON.stringify(metadata));
    return true;
  } catch {
    return false;
  }
}

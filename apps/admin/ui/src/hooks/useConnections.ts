import { useEffect, useState } from 'react';
import { checkConnection } from '../connections/client';
import { loadConnections, saveConnections } from '../connections/storage';
import type { Connection, ConnectionCheck, Credentials } from '../connections/types';

export const HEALTH_INTERVAL_MS = 30_000;

export function useConnections() {
  const [connections, setConnections] = useState(loadConnections);
  const [credentials, setCredentials] = useState<Record<string, Credentials>>({});
  const [checks, setChecks] = useState<Record<string, ConnectionCheck>>({});
  const [storageAvailable, setStorageAvailable] = useState(true);
  const [revision, setRevision] = useState(0);
  const [refreshing, setRefreshing] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;

    async function poll() {
      setRefreshing(connections.length > 0);
      setChecks(previous => Object.fromEntries(connections.map(connection => [connection.id,
        previous[connection.id] ?? { status: 'checking', api: 'unknown', database: 'unknown' },
      ])));
      await Promise.all(connections.map(async connection => {
        try {
          const check = await checkConnection(connection.url, credentials[connection.id], controller.signal);
          if (controller.signal.aborted) return;
          setChecks(previous => ({ ...previous, [connection.id]: check }));
          if (check.status === 'authentication' && credentials[connection.id]) {
            setCredentials(previous => {
              const next = { ...previous };
              delete next[connection.id];
              return next;
            });
          }
        } catch {
          if (!controller.signal.aborted) {
            setChecks(previous => ({ ...previous, [connection.id]: {
              status: 'unreachable', api: 'unavailable', database: 'unknown', checkedAt: Date.now(),
            } }));
          }
        }
      }));
      if (!controller.signal.aborted) {
        setRefreshing(false);
        if (connections.length > 0) timer = setTimeout(poll, HEALTH_INTERVAL_MS);
      }
    }

    void poll();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [connections, credentials, revision]);

  function save(connection: Connection, credential: Credentials, check: ConnectionCheck) {
    const next = connections.some(item => item.id === connection.id)
      ? connections.map(item => item.id === connection.id ? connection : item)
      : [...connections, connection];
    setStorageAvailable(saveConnections(next));
    setConnections(next);
    setCredentials(previous => ({ ...previous, [connection.id]: credential }));
    setChecks(previous => ({ ...previous, [connection.id]: check }));
  }

  function remove(id: string) {
    const next = connections.filter(connection => connection.id !== id);
    setStorageAvailable(saveConnections(next));
    setConnections(next);
    forgetCredentials(id);
  }

  function forgetCredentials(id: string) {
    setChecks(previous => ({ ...previous, [id]: {
      ...(previous[id] ?? { api: 'unknown', database: 'unknown' }), status: 'authentication',
    } }));
    setCredentials(previous => {
      const next = { ...previous };
      delete next[id];
      return next;
    });
  }

  return { connections, credentials, checks, storageAvailable, refreshing, save, remove, forgetCredentials, refresh: () => setRevision(value => value + 1) };
}

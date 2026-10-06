import { useEffect, useMemo, useRef, useState } from 'react';
import { checkConnection } from '../connections/client';
import { deleteSession, listSessions } from '../connections/sessions';
import { loadConnections, saveConnections } from '../connections/storage';
import type { Connection, ConnectionCheck, Session } from '../connections/types';

export const HEALTH_INTERVAL_MS = 30_000;

export function useConnections() {
  const [connections, setConnections] = useState(loadConnections);
  const [sessions, setSessions] = useState<Record<string, Session>>({});
  const [checks, setChecks] = useState<Record<string, ConnectionCheck>>({});
  const [storageAvailable, setStorageAvailable] = useState(true);
  const [revision, setRevision] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const [restoring, setRestoring] = useState(true);
  const [restoreFailed, setRestoreFailed] = useState(false);
  const changedURLs = useRef(new Set<string>());
  const currentSessions = useRef(sessions);
  currentSessions.current = sessions;
  const credentials = useMemo(() => Object.fromEntries(connections.filter(connection => sessions[connection.url])
    .map(connection => [connection.id, sessions[connection.url]])), [connections, sessions]);

  function clearSession(url: string) {
    changedURLs.current.add(url);
    setSessions(previous => {
      if (!previous[url]) return previous;
      const next = { ...previous }; delete next[url]; return next;
    });
  }

  useEffect(() => {
    const controller = new AbortController();
    void listSessions(controller.signal).then(items => {
      if (controller.signal.aborted) return;
      setSessions(previous => ({ ...Object.fromEntries(items.filter(item => !changedURLs.current.has(item.url)).map(item => [item.url, item])), ...previous }));
      setRestoreFailed(false);
      setRestoring(false);
    }).catch(() => {
      if (!controller.signal.aborted) { setRestoreFailed(true); setRestoring(false); }
    });
    const expire = (event: Event) => {
      const url = (event as CustomEvent<string>).detail;
      const observed = currentSessions.current[url];
      void listSessions(controller.signal).then(items => {
        if (controller.signal.aborted) return;
        setRestoreFailed(false);
        const restored = items.find(item => item.url === url);
        setSessions(previous => {
          if (previous[url] !== observed) return previous;
          const next = { ...previous };
          if (restored) next[url] = restored;
          else { changedURLs.current.add(url); delete next[url]; }
          return next;
        });
      }).catch(() => { if (!controller.signal.aborted) setRestoreFailed(true); });
    };
    window.addEventListener('commander:session-expired', expire);
    return () => { controller.abort(); window.removeEventListener('commander:session-expired', expire); };
  }, []);

  useEffect(() => {
    if (restoring) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      setRefreshing(connections.length > 0);
      setChecks(previous => Object.fromEntries(connections.map(connection => [connection.id,
        previous[connection.id] ?? { status: 'checking', api: 'unknown', database: 'unknown' },
      ])));
      await Promise.all(connections.map(async connection => {
        try {
          const check = await checkConnection(connection.url, sessions[connection.url], controller.signal);
          if (!controller.signal.aborted) setChecks(previous => ({ ...previous, [connection.id]: check }));
        } catch {
          if (!controller.signal.aborted) setChecks(previous => ({ ...previous, [connection.id]: {
            status: 'unreachable', api: 'unavailable', database: 'unknown', checkedAt: Date.now(),
          } }));
        }
      }));
      if (!controller.signal.aborted) {
        setRefreshing(false);
        if (connections.length > 0) timer = setTimeout(poll, HEALTH_INTERVAL_MS);
      }
    }
    void poll();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [connections, sessions, revision, restoring]);

  async function save(connection: Connection, session: Session) {
    const previous = connections.find(item => item.id === connection.id);
    if (previous && previous.url !== connection.url) {
      try { await deleteSession(previous.url); }
      catch (error) { await deleteSession(connection.url).catch(() => {}); throw error; }
      clearSession(previous.url);
    }
    const next = previous ? connections.map(item => item.id === connection.id ? connection : item) : [...connections, connection];
    setStorageAvailable(saveConnections(next));
    setConnections(next);
    setRestoreFailed(false);
    changedURLs.current.add(session.url);
    setSessions(previous => ({ ...previous, [session.url]: session }));
    setChecks(previous => ({ ...previous, [connection.id]: { status: 'ready', api: 'ok', database: 'ok', checkedAt: Date.now() } }));
  }

  async function forgetSession(id: string) {
    const connection = connections.find(item => item.id === id);
    if (!connection) return;
    await deleteSession(connection.url);
    clearSession(connection.url);
    setChecks(previous => ({ ...previous, [id]: {
      ...(previous[id] ?? { api: 'unknown', database: 'unknown' }), status: 'authentication',
    } }));
  }

  async function remove(id: string) {
    await forgetSession(id);
    const next = connections.filter(connection => connection.id !== id);
    setStorageAvailable(saveConnections(next));
    setConnections(next);
  }

  return { connections, credentials, checks, storageAvailable, restoring, restoreFailed, refreshing, save, remove, forgetSession, refresh: () => setRevision(value => value + 1) };
}

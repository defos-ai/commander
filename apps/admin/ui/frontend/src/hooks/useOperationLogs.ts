import { useEffect, useRef, useState } from 'react';
import { ApiError } from '../api/client';
import { listOperationLogs, type OperationLog, type OperationLogFilters } from '../api/operation-logs';
import type { Session } from '../connections/types';

const pageSize = 100;
const maxEntries = 1000;
const pollInterval = 2000;
type Read = 'latest' | 'newer' | 'older';
type Source = { url: string; credentials: Session; operationID: string } & OperationLogFilters;
type State = Source & {
  entries: OperationLog[];
  initialized: boolean;
  following: boolean;
  fetching: boolean;
  loadingOlder: boolean;
  hasOlder: boolean;
  truncated: boolean;
  error?: ApiError;
};
type Controls = { pause: () => void; resume: () => void; older: () => void; latest: () => void; retry: () => void };

export function useOperationLogs(url: string, credentials: Session | undefined, operationID: string, filters: OperationLogFilters) {
  const [result, setResult] = useState<State>();
  const controls = useRef<Controls | undefined>(undefined);
  const { task_id, agent_id, action_name } = filters;

  useEffect(() => {
    if (!credentials) return;
    const source: Source = { url, credentials, operationID, task_id, agent_id, action_name };
    let state: State = { ...source, entries: [], initialized: false, following: true, fetching: false,
      loadingOlder: false, hasOlder: false, truncated: false };
    let stopped = false;
    let active: AbortController | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let failedRead: Read | undefined;
    let catchupPages = 0;
    const publish = () => { if (!stopped) setResult({ ...state }); };
    const visible = () => document.visibilityState !== 'hidden';
    function abortRead() {
      clearTimeout(timer);
      const pending = active;
      active = undefined;
      pending?.abort();
      state = { ...state, fetching: false, loadingOlder: false };
    }
    async function load(kind: Read) {
      if (stopped || active || !visible()) return;
      const request = new AbortController();
      active = request;
      const cursor = kind === 'latest' ? '' : kind === 'older' ? state.entries[0]?.id ?? '' : state.entries.at(-1)?.id ?? '';
      state = { ...state, fetching: true, loadingOlder: kind === 'older', error: undefined };
      publish();
      let retry = true;
      let delay = pollInterval;
      try {
        const page = await listOperationLogs(url, source.credentials, operationID, {
          task_id, agent_id, action_name, order: kind === 'newer' ? 'asc' : 'desc', cursor, limit: pageSize,
        }, request.signal);
        if (stopped || active !== request || request.signal.aborted) return;
        if (page.items.length > 0 && page.next_cursor === cursor) throw new ApiError('invalid_response');
        if (kind !== 'latest' && page.items.length) {
          const retained = new Set(state.entries.map(entry => entry.id));
          if (page.items.some(entry => retained.has(entry.id))) throw new ApiError('invalid_response');
        }
        const incoming = kind === 'newer' ? page.items : [...page.items].reverse();
        const combined = kind === 'latest' ? incoming : kind === 'older' ? [...incoming, ...state.entries] : [...state.entries, ...incoming];
        const trimmed = combined.length > maxEntries;
        const entries = kind === 'older' ? combined.slice(0, maxEntries) : combined.slice(-maxEntries);
        state = { ...state, entries, initialized: true,
          hasOlder: kind === 'newer' ? state.hasOlder || trimmed : page.items.length === pageSize,
          truncated: kind === 'latest' ? false : state.truncated || trimmed };
        failedRead = undefined;
        // Yield after one window of catch-up so sustained producers cannot spin the viewer.
        if (kind === 'newer' && page.items.length === pageSize && ++catchupPages < maxEntries / pageSize) delay = 0;
        else catchupPages = 0;
      } catch (cause) {
        if (stopped || active !== request || request.signal.aborted) return;
        const error = cause instanceof ApiError ? cause : new ApiError('unavailable');
        state = { ...state, error };
        failedRead = kind;
        retry = error.kind === 'unavailable' || error.kind === 'timeout';
      } finally {
        if (!stopped && active === request) {
          active = undefined;
          state = { ...state, fetching: false, loadingOlder: false };
          publish();
          if (state.following && retry && visible()) {
            timer = setTimeout(() => { void load(state.initialized ? 'newer' : 'latest'); }, delay);
          }
        }
      }
    }
    function pause() {
      abortRead();
      state = { ...state, following: false };
      publish();
    }
    function resume() {
      abortRead();
      state = { ...state, following: true };
      publish();
      void load(state.initialized ? 'newer' : 'latest');
    }
    const actions: Controls = {
      pause, resume,
      older() {
        if (!state.initialized || !state.hasOlder || !state.entries.length) return;
        pause();
        void load('older');
      },
      latest() {
        abortRead();
        state = { ...state, following: true };
        publish();
        void load('latest');
      },
      retry() {
        abortRead();
        void load(failedRead ?? (state.initialized ? 'newer' : 'latest'));
      },
    };
    controls.current = actions;
    function visibilityChanged() {
      if (!visible()) { abortRead(); publish(); }
      else if (state.following) void load(state.initialized ? 'newer' : 'latest');
    }
    document.addEventListener('visibilitychange', visibilityChanged);
    publish();
    void load('latest');
    return () => {
      stopped = true;
      abortRead();
      document.removeEventListener('visibilitychange', visibilityChanged);
      if (controls.current === actions) controls.current = undefined;
    };
  }, [url, credentials, operationID, task_id, agent_id, action_name]);

  const current = credentials && result?.url === url && result.credentials === credentials && result.operationID === operationID &&
    result.task_id === task_id && result.agent_id === agent_id && result.action_name === action_name ? result : undefined;
  return {
    entries: current?.entries ?? [], error: current?.error,
    loading: Boolean(credentials && (!current || !current.initialized && current.fetching)),
    fetching: current?.fetching ?? false, loadingOlder: current?.loadingOlder ?? false,
    following: current?.following ?? Boolean(credentials), hasOlder: current?.hasOlder ?? false, truncated: current?.truncated ?? false,
    pause: () => controls.current?.pause(), resume: () => controls.current?.resume(),
    loadOlder: () => controls.current?.older(), jumpToLatest: () => controls.current?.latest(), retry: () => controls.current?.retry(),
  };
}

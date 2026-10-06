import { useEffect, useState } from 'react';
import { ApiError } from '../api/client';
import type { Credentials } from '../connections/types';

type CatalogSource<T> = {
  url: string;
  credentials?: Credentials;
  id?: string;
  list: (url: string, credentials: Credentials, signal?: AbortSignal) => Promise<T[]>;
  get: (url: string, credentials: Credentials, id: string, signal?: AbortSignal) => Promise<T>;
  pollInterval?: number;
};
type Result<T> = CatalogSource<T> & {
  revision: number;
  items?: T[];
  item?: T;
  error?: ApiError;
  checkedAt?: number;
  refreshing: boolean;
};

export function useCatalogQuery<T>({ url, credentials, id, list, get, pollInterval }: CatalogSource<T>) {
  const [revision, setRevision] = useState(0);
  const [result, setResult] = useState<Result<T>>();

  useEffect(() => {
    if (!credentials) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const source = { url, credentials, id, list, get, revision };
    async function load() {
      let retry = true;
      setResult(previous => previous?.url === url && previous.credentials === credentials && previous.id === id &&
        previous.list === list && previous.get === get && previous.revision === revision
        ? { ...previous, refreshing: true } : { ...source, refreshing: true });
      try {
        const data = id === undefined
          ? { items: await list(url, source.credentials, controller.signal) }
          : { item: await get(url, source.credentials, id, controller.signal) };
        if (!controller.signal.aborted) setResult({ ...source, ...data, refreshing: false, checkedAt: Date.now() });
      } catch (cause) {
        const error = cause instanceof ApiError ? cause : new ApiError('unavailable');
        retry = !['authentication', 'forbidden', 'not_found'].includes(error.kind);
        if (!controller.signal.aborted) setResult({ ...source, error, refreshing: false, checkedAt: Date.now() });
      } finally {
        if (!controller.signal.aborted && retry && pollInterval && pollInterval > 0) timer = setTimeout(load, pollInterval);
      }
    }
    void load();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [url, credentials, id, list, get, revision, pollInterval]);

  const current = credentials && result?.url === url && result.credentials === credentials && result.id === id &&
    result.list === list && result.get === get && result.revision === revision ? result : undefined;
  return {
    items: current?.items,
    item: current?.item,
    error: current?.error,
    loading: Boolean(credentials && (!current || current.refreshing && !current.items && !current.item)),
    refreshing: Boolean(credentials && (!current || current.refreshing)),
    checkedAt: current?.checkedAt,
    refresh: () => setRevision(value => value + 1),
  };
}

import { useEffect, useState } from 'react';
import { ApiError } from '../api/client';
import type { OperationGraph } from '../api/graph-types';
import { getOperationGraph } from '../api/operation-graph';
import type { Credentials } from '../connections/types';

type Result = {
  url: string;
  credentials: Credentials;
  operationID: string;
  graph?: OperationGraph;
  error?: ApiError;
  refreshing: boolean;
};

export function useOperationGraph(url: string, credentials: Credentials | undefined, operationID: string) {
  const [revision, setRevision] = useState(0);
  const [result, setResult] = useState<Result>();

  useEffect(() => {
    if (!credentials) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const source = { url, credentials, operationID };
    async function load() {
      let retry = true;
      setResult(previous => previous?.url === url && previous.credentials === credentials && previous.operationID === operationID
        ? { ...previous, refreshing: true } : { ...source, refreshing: true });
      try {
        const graph = await getOperationGraph(url, source.credentials, operationID, controller.signal);
        if (!controller.signal.aborted) setResult({ ...source, graph, refreshing: false });
      } catch (cause) {
        const error = cause instanceof ApiError ? cause : new ApiError('unavailable');
        retry = !['authentication', 'forbidden', 'not_found'].includes(error.kind);
        if (!controller.signal.aborted) setResult({ ...source, error, refreshing: false });
      } finally {
        if (!controller.signal.aborted && retry) timer = setTimeout(load, 10_000);
      }
    }
    void load();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [url, credentials, operationID, revision]);

  const current = credentials && result?.url === url && result.credentials === credentials && result.operationID === operationID ? result : undefined;
  return {
    graph: current?.graph,
    error: current?.error,
    refreshing: Boolean(credentials && (!current || current.refreshing)),
    refresh: () => setRevision(value => value + 1),
  };
}

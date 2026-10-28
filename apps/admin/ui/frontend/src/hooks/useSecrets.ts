import { useEffect, useState } from 'react';
import { ApiError } from '../api/client';
import { listSecretRequests, listSecrets, SecretError, type PendingSecretRequest, type SecretMetadata } from '../api/secrets';
import type { Session } from '../connections/types';

type Source = { url: string; credentials?: Session };
type Snapshot = Source & {
  revision: number;
  requests?: PendingSecretRequest[];
  items?: SecretMetadata[];
  error?: SecretError;
  listError?: SecretError;
  refreshing: boolean;
  checkedAt?: number;
};

function asError(cause: unknown): SecretError {
  return cause instanceof SecretError ? cause : new SecretError(cause instanceof ApiError ? cause.kind : 'unavailable');
}

export function useSecrets({ url, credentials }: Source) {
  const [revision, setRevision] = useState(0);
  const [result, setResult] = useState<Snapshot>();

  useEffect(() => {
    if (!credentials) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const source = { url, credentials, revision };
    async function load() {
      setResult(previous => previous?.url === url && previous.credentials === credentials && previous.revision === revision
        ? { ...previous, refreshing: true } : { ...source, refreshing: true });
      const [requests, items] = await Promise.allSettled([
        listSecretRequests(url, source.credentials, controller.signal),
        listSecrets(url, source.credentials, controller.signal),
      ]);
      if (controller.signal.aborted) return;
      const pending = requests.status === 'fulfilled' ? requests.value : undefined;
      const error = requests.status === 'rejected' ? asError(requests.reason) : undefined;
      const listError = items.status === 'rejected' ? asError(items.reason) : undefined;
      setResult({ ...source, requests: pending, items: items.status === 'fulfilled' ? items.value : undefined,
        error, listError, refreshing: false, checkedAt: Date.now() });
      if (![error, listError].some(value => value && ['authentication', 'forbidden', 'disabled', 'not_found'].includes(value.kind))) {
        timer = setTimeout(() => { void load(); }, 10_000);
      }
    }
    void load();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [url, credentials, revision]);

  const current = credentials && result?.url === url && result.credentials === credentials && result.revision === revision ? result : undefined;
  return {
    requests: current?.requests,
    items: current?.items,
    error: current?.error,
    listError: current?.listError,
    loading: Boolean(credentials && (!current || current.refreshing && !current.requests)),
    refreshing: Boolean(credentials && (!current || current.refreshing)),
    checkedAt: current?.checkedAt,
    refresh: () => setRevision(value => value + 1),
  };
}

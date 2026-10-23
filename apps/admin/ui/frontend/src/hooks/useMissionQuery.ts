import { useEffect, useState } from 'react';
import { ApiError } from '../api/client';
import { getMissionDocument, listMissions } from '../api/missions';
import type { Mission } from '../api/types';
import type { Session } from '../connections/types';

type Content = { items: Mission[] } | { mission: Mission; definition: string };
type Result = {
  url: string;
  credentials: Session;
  name?: string;
  revision: number;
  content?: Content;
  error?: ApiError;
};

export function useMissionQuery(url: string, credentials: Session | undefined, name?: string) {
  const [revision, setRevision] = useState(0);
  const [result, setResult] = useState<Result>();

  useEffect(() => {
    if (!credentials) return;
    const controller = new AbortController();
    const source = { url, credentials, name, revision };
    async function load() {
      try {
        const content: Content = name === undefined
          ? { items: await listMissions(url, source.credentials, controller.signal) }
          : await getMissionDocument(url, source.credentials, name, controller.signal);
        if (!controller.signal.aborted) setResult({ ...source, content });
      } catch (error) {
        if (!controller.signal.aborted) setResult({ ...source,
          error: error instanceof ApiError ? error : new ApiError('unavailable'),
        });
      }
    }
    void load();
    return () => controller.abort();
  }, [url, credentials, name, revision]);

  const current = credentials && result?.url === url && result.credentials === credentials &&
    result.name === name && result.revision === revision ? result : undefined;
  return {
    content: current?.content,
    error: current?.error,
    loading: Boolean(credentials && !current),
    refresh: () => setRevision(value => value + 1),
  };
}

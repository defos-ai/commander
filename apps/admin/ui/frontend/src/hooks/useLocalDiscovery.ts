import { useEffect, useState } from 'react';
import { checkConnection } from '../connections/client';

export const LOCAL_API_URL = 'http://localhost:4274';

export function useLocalDiscovery(savedURLs: string[]) {
  const [status, setStatus] = useState<'checking' | 'found' | 'absent'>('checking');
  const saved = savedURLs.includes(LOCAL_API_URL);
  useEffect(() => {
    if (saved) { setStatus('absent'); return; }
    const controller = new AbortController();
    setStatus('checking');
    void checkConnection(LOCAL_API_URL, undefined, controller.signal).then(check => {
      if (!controller.signal.aborted) setStatus(check.api === 'ok' ? 'found' : 'absent');
    }).catch(() => {
      if (!controller.signal.aborted) setStatus('absent');
    });
    return () => controller.abort();
  }, [saved]);
  return { status, url: LOCAL_API_URL };
}

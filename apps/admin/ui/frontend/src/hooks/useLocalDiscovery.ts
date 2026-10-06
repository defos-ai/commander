import { useEffect, useState } from 'react';
import { checkConnection } from '../connections/client';

export const LOCAL_API_URL = 'http://localhost:4274';

export function useLocalDiscovery(saved: boolean) {
  const [status, setStatus] = useState<'checking' | 'found' | 'absent'>('checking');
  useEffect(() => {
    if (saved) return;
    const controller = new AbortController();
    setStatus('checking');
    void checkConnection(LOCAL_API_URL, undefined, controller.signal).then(check => {
      if (!controller.signal.aborted) setStatus(check.api === 'ok' ? 'found' : 'absent');
    }).catch(() => {
      if (!controller.signal.aborted) setStatus('absent');
    });
    return () => controller.abort();
  }, [saved]);
  return saved ? 'absent' : status;
}

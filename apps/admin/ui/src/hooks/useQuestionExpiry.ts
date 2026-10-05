import { useEffect, useState } from 'react';

export function useQuestionExpiry(expiresAt: string) {
  const [now, setNow] = useState(Date.now);
  const deadline = expiresAt.startsWith('0001-') ? Infinity : Date.parse(expiresAt);
  useEffect(() => {
    const remaining = deadline - Date.now();
    if (!Number.isFinite(remaining)) return;
    if (remaining <= 0) return;
    const timer = setTimeout(() => setNow(Date.now()), Math.min(remaining + 1, 2_147_483_647));
    return () => clearTimeout(timer);
  }, [deadline, now]);
  return Number.isFinite(deadline) && deadline <= Math.max(now, Date.now());
}

import { useCallback, useEffect, useState } from 'react';
import { parseRoute, routeHash, type Route } from '../navigation/routes';

export function useRoute() {
  const [route, setRoute] = useState(() => parseRoute(window.location.hash));

  useEffect(() => {
    function changed() {
      const next = parseRoute(window.location.hash);
      setRoute(previous => routeHash(previous) === routeHash(next) ? previous : next);
    }
    window.addEventListener('popstate', changed);
    window.addEventListener('hashchange', changed);
    return () => {
      window.removeEventListener('popstate', changed);
      window.removeEventListener('hashchange', changed);
    };
  }, []);

  const navigate = useCallback((next: Route, options?: { replace: boolean }) => {
    const hash = routeHash(next);
    if (window.location.hash !== hash) {
      if (options?.replace) window.history.replaceState(null, '', hash);
      else window.history.pushState(null, '', hash);
    }
    setRoute(previous => routeHash(previous) === hash ? previous : next);
  }, []);

  return { route, navigate };
}

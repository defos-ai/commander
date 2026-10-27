import { parseConnectionPrefill, validOperationID, type ConnectionPrefill } from './connection-link';
import type { SessionFilters } from '../api/runtime-sessions';

export type WorkspaceSection = 'overview' | 'operations' | 'missions' | 'create-mission' | 'controllers' | 'agents' | 'actions' | 'sessions' | 'secrets' | 'settings';
export type OperationView = 'details' | 'graph';
export type MissionView = 'details' | 'json';

export type Route = { connectionID?: undefined; connect?: ConnectionPrefill | null; operation?: string } | {
  connectionID: string;
  connect?: undefined;
  operation?: undefined;
  section: WorkspaceSection;
  itemID?: string;
  view?: OperationView | MissionView;
  sessionFilters?: SessionFilters;
};

const sections: readonly string[] = ['overview', 'operations', 'missions', 'create-mission', 'controllers', 'agents', 'actions', 'sessions', 'secrets', 'settings'];

export function parseRoute(hash: string): Route {
  if (hash === '#/connect' || hash.startsWith('#/connect?')) {
    return { connect: parseConnectionPrefill(hash.slice('#/connect?'.length)) };
  }
  try {
    const queryStart = hash.indexOf('?');
    const path = queryStart < 0 ? hash : hash.slice(0, queryStart);
    const [prefix, collection, connectionID, section, itemID, view, ...extra] = path.slice(1).split('/').map(decodeURIComponent);
    if (prefix === '' && collection === 'operations' && connectionID && validOperationID(connectionID)
      && section === undefined && !/[?#]/u.test(hash.slice(1))) return { operation: connectionID };
    if (prefix !== '' || collection !== 'connections' || !connectionID || !sections.includes(section ?? '') || extra.length) return {};
    if (itemID !== undefined && (!itemID || section === 'overview' || section === 'settings' || section === 'secrets' || section === 'create-mission')) return {};
    if (view !== undefined && (!itemID || !(section === 'operations' && view === 'graph' || section === 'missions' && view === 'json'))) return {};
    const sessionFilters: SessionFilters = {};
    if (queryStart >= 0) {
      if (section !== 'sessions') return {};
      const query = new URLSearchParams(hash.slice(queryStart + 1));
      for (const [key, value] of query) {
        if ((key !== 'scope_id' && key !== 'owner_agent_id') || !value || query.getAll(key).length !== 1) return {};
        sessionFilters[key] = value;
      }
    }
    return { connectionID, section: section as WorkspaceSection, ...(itemID ? { itemID } : {}), ...(view ? { view: view as 'graph' | 'json' } : {}),
      ...(Object.keys(sessionFilters).length ? { sessionFilters } : {}) };
  } catch {
    return {};
  }
}

export function routeHash(route: Route): string {
  if (!route.connectionID && route.connect !== undefined) {
    return route.connect ? `#/connect?${new URLSearchParams({ api: route.connect.url, username: route.connect.username,
      ...(route.connect.operation ? { operation: route.connect.operation } : {}) })}` : '#/connect';
  }
  if (!route.connectionID && route.operation) return `#/operations/${encodeURIComponent(route.operation)}`;
  if (!route.connectionID) return '#/';
  const parts = ['connections', route.connectionID, route.section];
  if (route.itemID) {
    parts.push(route.itemID);
    if (route.section === 'operations' && route.view === 'graph' || route.section === 'missions' && route.view === 'json') parts.push(route.view);
  }
  const query = new URLSearchParams();
  if (route.section === 'sessions') {
    if (route.sessionFilters?.scope_id) query.set('scope_id', route.sessionFilters.scope_id);
    if (route.sessionFilters?.owner_agent_id) query.set('owner_agent_id', route.sessionFilters.owner_agent_id);
  }
  return `#/${parts.map(encodeURIComponent).join('/')}${query.size ? `?${query}` : ''}`;
}

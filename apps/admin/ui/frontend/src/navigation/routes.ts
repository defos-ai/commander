import { parseConnectionPrefill, validOperationID, type ConnectionPrefill } from './connection-link';

export type WorkspaceSection = 'overview' | 'operations' | 'missions' | 'create-mission' | 'controllers' | 'agents' | 'actions' | 'secrets' | 'settings';
export type OperationView = 'details' | 'graph';
export type MissionView = 'details' | 'json';

export type Route = { connectionID?: undefined; connect?: ConnectionPrefill | null; operation?: string } | {
  connectionID: string;
  connect?: undefined;
  operation?: undefined;
  section: WorkspaceSection;
  itemID?: string;
  view?: OperationView | MissionView;
};

const sections: readonly string[] = ['overview', 'operations', 'missions', 'create-mission', 'controllers', 'agents', 'actions', 'secrets', 'settings'];

export function parseRoute(hash: string): Route {
  if (hash === '#/connect' || hash.startsWith('#/connect?')) {
    return { connect: parseConnectionPrefill(hash.slice('#/connect?'.length)) };
  }
  try {
    const [prefix, collection, connectionID, section, itemID, view, ...extra] = hash.slice(1).split('/').map(decodeURIComponent);
    if (prefix === '' && collection === 'operations' && connectionID && validOperationID(connectionID)
      && section === undefined && !/[?#]/u.test(hash.slice(1))) return { operation: connectionID };
    if (prefix !== '' || collection !== 'connections' || !connectionID || !sections.includes(section ?? '') || extra.length) return {};
    if (itemID !== undefined && (!itemID || section === 'overview' || section === 'settings' || section === 'secrets' || section === 'create-mission')) return {};
    if (view !== undefined && (!itemID || !(section === 'operations' && view === 'graph' || section === 'missions' && view === 'json'))) return {};
    return { connectionID, section: section as WorkspaceSection, ...(itemID ? { itemID } : {}), ...(view ? { view: view as 'graph' | 'json' } : {}) };
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
  return `#/${parts.map(encodeURIComponent).join('/')}`;
}

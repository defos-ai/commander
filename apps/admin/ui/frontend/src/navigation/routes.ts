export type WorkspaceSection = 'overview' | 'operations' | 'missions' | 'agents' | 'actions' | 'settings';
export type OperationView = 'details' | 'graph';

export type Route = { connectionID?: undefined } | {
  connectionID: string;
  section: WorkspaceSection;
  itemID?: string;
  view?: OperationView;
};

const sections: readonly string[] = ['overview', 'operations', 'missions', 'agents', 'actions', 'settings'];

export function parseRoute(hash: string): Route {
  try {
    const [prefix, collection, connectionID, section, itemID, view, ...extra] = hash.slice(1).split('/').map(decodeURIComponent);
    if (prefix !== '' || collection !== 'connections' || !connectionID || !sections.includes(section ?? '') || extra.length) return {};
    if (itemID !== undefined && (!itemID || section === 'overview' || section === 'settings')) return {};
    if (view !== undefined && (view !== 'graph' || section !== 'operations' || !itemID)) return {};
    return { connectionID, section: section as WorkspaceSection, ...(itemID ? { itemID } : {}), ...(view ? { view: 'graph' as const } : {}) };
  } catch {
    return {};
  }
}

export function routeHash(route: Route): string {
  if (!route.connectionID) return '#/';
  const parts = ['connections', route.connectionID, route.section];
  if (route.itemID) {
    parts.push(route.itemID);
    if (route.section === 'operations' && route.view === 'graph') parts.push('graph');
  }
  return `#/${parts.map(encodeURIComponent).join('/')}`;
}

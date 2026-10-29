import { useEffect, useRef, useState } from 'react';
import { LoaderCircle } from 'lucide-react';
import { Button } from './components/Button';
import { ConnectionDialog } from './components/ConnectionDialog';
import { Dialog } from './components/Dialog';
import { WorkspaceLayout } from './components/WorkspaceLayout';
import { normalizeURL } from './connections/client';
import { createSession, SessionError } from './connections/sessions';
import type { Connection } from './connections/types';
import { useConnections } from './hooks/useConnections';
import { useLocalDiscovery } from './hooks/useLocalDiscovery';
import { useRoute } from './hooks/useRoute';
import { useTranslation } from './i18n';
import { routeHash, type Route } from './navigation/routes';
import { ConnectionsPage } from './pages/ConnectionsPage';
import { ConnectionSettingsPage } from './pages/ConnectionSettingsPage';
import { StatusPage } from './pages/StatusPage';
import { MissionsPage } from './pages/MissionsPage';
import { CreateMissionPage } from './pages/CreateMissionPage';
import { EditMissionPage } from './pages/EditMissionPage';
import { AgentsPage } from './pages/AgentsPage';
import { ControllersPage } from './pages/ControllersPage';
import { ActionsPage } from './pages/ActionsPage';
import { OperationsPage } from './pages/OperationsPage';
import { SecretsPage } from './pages/SecretsPage';
import { SessionsPage } from './pages/SessionsPage';

type Modal = { kind: 'connect'; connection?: Connection; localURL?: string; username?: string } | { kind: 'remove'; connection: Connection };

export function App() {
  const { t } = useTranslation();
  const state = useConnections();
  const { route, navigate } = useRoute();
  const activeID = route.connectionID;
  const operationLinkID = route.operation;
  const pendingConnectionLink = !route.connectionID && route.connect !== undefined;
  const section = route.connectionID ? route.section : 'operations';
  const selectedID = route.connectionID ? route.itemID : undefined;
  const [modal, setModal] = useState<Modal>();
  const [removing, setRemoving] = useState(false);
  const [removeFailed, setRemoveFailed] = useState(false);
  const focusAfterTransition = useRef(false);
  const previousRoute = useRef(routeHash(route));
  const active = state.connections.find(connection => connection.id === activeID);
  const local = useLocalDiscovery(state.connections.map(connection => connection.url));

  useEffect(() => {
    const hash = routeHash(route);
    if (previousRoute.current !== hash) {
      previousRoute.current = hash;
      focusAfterTransition.current = true;
      setModal(undefined);
    }
    if (route.connectionID || route.connect === undefined || (route.connect && state.restoring)) return;
    const prefill = route.connect;
    const connection = prefill && state.connections.find(item => item.url === prefill.url);
    const destination: Route = connection
      ? { connectionID: connection.id, section: 'operations', itemID: prefill.operation }
      : prefill?.operation ? { operation: prefill.operation } : {};
    // Consume the link once so polling, cancellation and browser history cannot reopen it.
    previousRoute.current = routeHash(destination);
    navigate(destination, { replace: true });
    if (prefill && (!connection || !state.credentials[connection.id])) {
      setModal({ kind: 'connect', connection: connection || undefined, localURL: prefill.url, username: prefill.username });
    }
  }, [route, navigate, state.restoring, state.connections, state.credentials]);

  useEffect(() => {
    if (modal || !focusAfterTransition.current) return;
    const editor = active && (section === 'create-mission' || section === 'missions' && route.connectionID && route.view === 'edit')
      ? document.querySelector<HTMLElement>('.mission-create-form [data-initial-focus]:not(:disabled)') : null;
    (editor ?? document.querySelector<HTMLElement>(active ? '.workspace-main' : '#connections-heading'))?.focus();
    focusAfterTransition.current = false;
  }, [route, active, modal]);

  function open(id: string) { navigate({ connectionID: id, section: 'operations', itemID: operationLinkID }); }
  function select(itemID?: string) {
    if (activeID) navigate({ connectionID: activeID, section, itemID,
      ...(route.connectionID && section === 'sessions' ? { sessionFilters: route.sessionFilters } : {}) });
  }
  function edit(id: string) { setModal({ kind: 'connect', connection: state.connections.find(connection => connection.id === id) }); }
  function remove(id: string) {
    const connection = state.connections.find(connection => connection.id === id);
    if (connection) { setRemoveFailed(false); setModal({ kind: 'remove', connection }); }
  }
  const storageNotice = !state.storageAvailable && <p className="notice" role="alert">{t($ => $.connections.storageError)}</p>;

  return <>
    {active ? <WorkspaceLayout connection={active} check={state.checks[active.id]} section={section} sections={['operations', 'missions', 'controllers', 'agents', 'sessions', 'actions', 'secrets', 'status', 'settings']}
      onNavigate={destination => navigate({ connectionID: active.id, section: destination })} onBack={() => navigate({})}>
      {storageNotice}
      {state.restoreFailed && <p className="notice" role="alert">{t($ => $.connections.restoreError)}</p>}
      {state.restoring ? <p role="status">{t($ => $.connections.restoring)}</p> : section === 'settings' ? <ConnectionSettingsPage connection={active} signedIn={Boolean(state.credentials[active.id])}
        onEdit={() => edit(active.id)} onForget={() => state.forgetSession(active.id)} onRemove={() => remove(active.id)} />
        : section === 'secrets' ? <SecretsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} />
        : section === 'sessions' ? <SessionsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)}
          selectedID={selectedID} onSelect={select} filters={route.connectionID ? route.sessionFilters : undefined}
          onFiltersChange={sessionFilters => navigate({ connectionID: active.id, section: 'sessions', sessionFilters })}
          onOpenAgent={id => navigate({ connectionID: active.id, section: 'agents', itemID: id })} />
        : section === 'operations' ? <OperationsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)}
          selectedID={selectedID} onSelect={select} view={route.connectionID && (route.view === 'graph' || route.view === 'logs') ? route.view : 'details'}
          logFilters={route.connectionID ? route.logFilters : undefined}
          onLogFiltersChange={logFilters => navigate({ connectionID: active.id, section: 'operations', itemID: selectedID, view: 'logs', logFilters })}
          onManageSecrets={() => navigate({ connectionID: active.id, section: 'secrets' })}
          onOpenSession={target => navigate({ connectionID: active.id, section: 'sessions', itemID: target.id,
            sessionFilters: { ...(target.scope_id ? { scope_id: target.scope_id } : {}), ...(target.owner_agent_id ? { owner_agent_id: target.owner_agent_id } : {}) } })}
          onViewChange={(view, logFilters) => navigate({ connectionID: active.id, section: 'operations', itemID: selectedID, view, logFilters })} />
        : section === 'create-mission' ? <CreateMissionPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)}
          onBack={() => navigate({ connectionID: active.id, section: 'missions' })}
          onSelect={name => navigate({ connectionID: active.id, section: 'missions', itemID: name })} />
        : section === 'missions' && route.connectionID && route.view === 'edit' && selectedID !== undefined
          ? <EditMissionPage key={`${active.id}:${active.url}:${selectedID}`} connection={active} credentials={state.credentials[active.id]} name={selectedID} onSignIn={() => edit(active.id)}
            onBack={() => navigate({ connectionID: active.id, section: 'missions', itemID: selectedID })}
            onSelect={name => navigate({ connectionID: active.id, section: 'missions', itemID: name })} />
        : section === 'missions' ? <MissionsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} selectedID={selectedID} onSelect={select}
          view={route.connectionID && route.view === 'json' ? 'json' : 'details'}
          onViewChange={view => navigate({ connectionID: active.id, section: 'missions', itemID: selectedID, view })}
          onCreate={() => navigate({ connectionID: active.id, section: 'create-mission' })} />
          : section === 'controllers' ? <ControllersPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} selectedID={selectedID} onSelect={select} />
          : section === 'agents' ? <AgentsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} selectedID={selectedID} onSelect={select}
            onManageSecrets={() => navigate({ connectionID: active.id, section: 'secrets' })}
            onOpenSessions={owner => navigate({ connectionID: active.id, section: 'sessions', sessionFilters: owner ? { owner_agent_id: owner } : undefined })} />
            : section === 'actions' ? <ActionsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} selectedID={selectedID} onSelect={select} />
              : <StatusPage check={state.checks[active.id]} refreshing={state.refreshing} onRefresh={state.refresh} onSignIn={() => edit(active.id)} />}
    </WorkspaceLayout> : <ConnectionsPage connections={state.connections} statuses={Object.fromEntries(Object.entries(state.checks).map(([id, check]) => [id, check.status]))}
      onOpen={open} onAdd={() => setModal({ kind: 'connect' })} onEdit={edit} onRemove={remove}>
      {storageNotice}
      {state.restoreFailed && <p className="notice" role="alert">{t($ => $.connections.restoreError)}</p>}
      {operationLinkID && <p className="notice" role="status">{t($ => $.connections.chooseOperationConnection, { id: operationLinkID })}</p>}
      {activeID && <p className="notice" role="status">{t($ => $.connections.missing)}</p>}
      {local.status === 'checking' && <p className="local-discovery" role="status"><LoaderCircle className="connection-form-spinner" size={14} aria-hidden="true" />{t($ => $.connections.localChecking)}</p>}
      {local.status === 'found' && <div className="local-discovery"><div><span className="status-dot" aria-hidden="true" />{t($ => $.connections.localFound)}<span className="local-url">{local.url}</span></div><Button variant="secondary" onClick={() => setModal({ kind: 'connect', localURL: local.url })}>{t($ => $.connections.localConnect)}</Button></div>}
    </ConnectionsPage>}
    {!pendingConnectionLink && modal?.kind === 'connect' && <ConnectionDialog connection={modal.connection} credentials={modal.connection ? state.credentials[modal.connection.id] : undefined} localURL={modal.localURL}
      initialUsername={modal.username}
      onClose={() => setModal(undefined)} onConnect={async (values, signal) => {
        const url = normalizeURL(values.url);
        if (state.connections.some(connection => connection.url === url && connection.id !== modal.connection?.id)) return t($ => $.connections.duplicate);
        let session;
        try { session = await createSession(url, { username: values.username, password: values.password }, signal); }
        catch (error) {
          if (error instanceof SessionError && error.kind === 'authentication') return t($ => $.connections.authenticationError);
          if (error instanceof SessionError && error.kind === 'forbidden') return t($ => $.connections.accessError);
          return t($ => $.connections.unreachableError);
        }
        if (signal.aborted) return;
        const id = modal.connection?.id ?? Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('');
        await state.save({ id, name: values.name, url }, session);
        if (signal.aborted) return;
        focusAfterTransition.current = true;
        setModal(undefined);
        if (activeID !== id) open(id);
      }} />}
    {modal?.kind === 'remove' && <Dialog open title={t($ => $.connections.removeTitle)} description={t($ => $.connections.removeDescription, { name: modal.connection.name })} onClose={() => setModal(undefined)}>
      {removeFailed && <p className="notice" role="alert">{t($ => $.connections.removeError)}</p>}
      <div className="dialog-actions">
        <Button variant="secondary" disabled={removing} onClick={() => setModal(undefined)}>{t($ => $.connectionForm.cancel)}</Button>
        <Button className="danger-button" disabled={removing} onClick={async () => {
          setRemoving(true); setRemoveFailed(false);
          try {
            await state.remove(modal.connection.id);
            focusAfterTransition.current = true;
            if (activeID === modal.connection.id) navigate({});
            setModal(undefined);
          } catch { setRemoveFailed(true); }
          finally { setRemoving(false); }
        }}>{t($ => $.connections.removeConfirm)}</Button>
      </div>
    </Dialog>}
  </>;
}

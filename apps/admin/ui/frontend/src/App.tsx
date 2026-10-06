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
import { ConnectionsPage } from './pages/ConnectionsPage';
import { ConnectionSettingsPage } from './pages/ConnectionSettingsPage';
import { OverviewPage } from './pages/OverviewPage';
import { MissionsPage } from './pages/MissionsPage';
import { AgentsPage } from './pages/AgentsPage';
import { ControllersPage } from './pages/ControllersPage';
import { ActionsPage } from './pages/ActionsPage';
import { OperationsPage } from './pages/OperationsPage';

type Modal = { kind: 'connect'; connection?: Connection; localURL?: string } | { kind: 'remove'; connection: Connection };

export function App() {
  const { t } = useTranslation();
  const state = useConnections();
  const { route, navigate } = useRoute();
  const activeID = route.connectionID;
  const section = route.connectionID ? route.section : 'overview';
  const selectedID = route.connectionID ? route.itemID : undefined;
  const [modal, setModal] = useState<Modal>();
  const [removing, setRemoving] = useState(false);
  const [removeFailed, setRemoveFailed] = useState(false);
  const focusAfterTransition = useRef(false);
  const previousRoute = useRef(route);
  const active = state.connections.find(connection => connection.id === activeID);
  const local = useLocalDiscovery(state.connections.map(connection => connection.url));

  useEffect(() => {
    if (previousRoute.current === route) return;
    previousRoute.current = route;
    focusAfterTransition.current = true;
    setModal(undefined);
  }, [route]);

  useEffect(() => {
    if (modal || !focusAfterTransition.current) return;
    document.querySelector<HTMLElement>(active ? '.workspace-main' : '#connections-heading')?.focus();
    focusAfterTransition.current = false;
  }, [route, active, modal]);

  function open(id: string) { navigate({ connectionID: id, section: 'overview' }); }
  function select(itemID?: string) {
    if (activeID) navigate({ connectionID: activeID, section, itemID });
  }
  function edit(id: string) { setModal({ kind: 'connect', connection: state.connections.find(connection => connection.id === id) }); }
  function remove(id: string) {
    const connection = state.connections.find(connection => connection.id === id);
    if (connection) { setRemoveFailed(false); setModal({ kind: 'remove', connection }); }
  }
  const storageNotice = !state.storageAvailable && <p className="notice" role="alert">{t($ => $.connections.storageError)}</p>;

  return <>
    {active ? <WorkspaceLayout connection={active} section={section} sections={['overview', 'operations', 'missions', 'controllers', 'agents', 'actions', 'settings']}
      onNavigate={destination => navigate({ connectionID: active.id, section: destination })} onBack={() => navigate({})}>
      {storageNotice}
      {state.restoreFailed && <p className="notice" role="alert">{t($ => $.connections.restoreError)}</p>}
      {state.restoring ? <p role="status">{t($ => $.connections.restoring)}</p> : section === 'settings' ? <ConnectionSettingsPage connection={active} signedIn={Boolean(state.credentials[active.id])}
        onEdit={() => edit(active.id)} onForget={() => state.forgetSession(active.id)} onRemove={() => remove(active.id)} />
        : section === 'operations' ? <OperationsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)}
          selectedID={selectedID} onSelect={select} view={route.connectionID ? route.view ?? 'details' : 'details'}
          onViewChange={view => navigate({ connectionID: active.id, section: 'operations', itemID: selectedID, view })} />
        : section === 'missions' ? <MissionsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} selectedID={selectedID} onSelect={select} />
          : section === 'controllers' ? <ControllersPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} selectedID={selectedID} onSelect={select} />
          : section === 'agents' ? <AgentsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} selectedID={selectedID} onSelect={select} />
            : section === 'actions' ? <ActionsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} selectedID={selectedID} onSelect={select} />
              : <OverviewPage check={state.checks[active.id]} refreshing={state.refreshing} onRefresh={state.refresh} onSignIn={() => edit(active.id)} />}
    </WorkspaceLayout> : <ConnectionsPage connections={state.connections} statuses={Object.fromEntries(Object.entries(state.checks).map(([id, check]) => [id, check.status]))}
      onOpen={open} onAdd={() => setModal({ kind: 'connect' })} onEdit={edit} onRemove={remove}>
      {storageNotice}
      {state.restoreFailed && <p className="notice" role="alert">{t($ => $.connections.restoreError)}</p>}
      {activeID && <p className="notice" role="status">{t($ => $.connections.missing)}</p>}
      {local.status === 'checking' && <p className="local-discovery" role="status"><LoaderCircle className="connection-form-spinner" size={14} aria-hidden="true" />{t($ => $.connections.localChecking)}</p>}
      {local.status === 'found' && <div className="local-discovery"><div><span className="status-dot" aria-hidden="true" />{t($ => $.connections.localFound)}<span className="local-url">{local.url}</span></div><Button variant="secondary" onClick={() => setModal({ kind: 'connect', localURL: local.url })}>{t($ => $.connections.localConnect)}</Button></div>}
    </ConnectionsPage>}
    {modal?.kind === 'connect' && <ConnectionDialog connection={modal.connection} credentials={modal.connection ? state.credentials[modal.connection.id] : undefined} localURL={modal.localURL}
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

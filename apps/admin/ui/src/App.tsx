import { useEffect, useRef, useState } from 'react';
import { LoaderCircle } from 'lucide-react';
import { Button } from './components/Button';
import { ConnectionDialog } from './components/ConnectionDialog';
import { Dialog } from './components/Dialog';
import { WorkspaceLayout, type WorkspaceSection } from './components/WorkspaceLayout';
import { checkConnection, normalizeURL } from './connections/client';
import type { Connection } from './connections/types';
import { useConnections } from './hooks/useConnections';
import { LOCAL_API_URL, useLocalDiscovery } from './hooks/useLocalDiscovery';
import { useTranslation } from './i18n';
import { ConnectionsPage } from './pages/ConnectionsPage';
import { ConnectionSettingsPage } from './pages/ConnectionSettingsPage';
import { OverviewPage } from './pages/OverviewPage';
import { MissionsPage } from './pages/MissionsPage';
import { AgentsPage } from './pages/AgentsPage';
import { ActionsPage } from './pages/ActionsPage';

type Modal = { kind: 'connect'; connection?: Connection } | { kind: 'remove'; connection: Connection };

export function App() {
  const { t } = useTranslation();
  const state = useConnections();
  const [activeID, setActiveID] = useState<string>();
  const [section, setSection] = useState<WorkspaceSection>('overview');
  const [modal, setModal] = useState<Modal>();
  const focusAfterTransition = useRef(false);
  const active = state.connections.find(connection => connection.id === activeID);
  const local = useLocalDiscovery(state.connections.some(connection => connection.url === LOCAL_API_URL));

  useEffect(() => {
    if (modal || !focusAfterTransition.current) return;
    document.querySelector<HTMLElement>(activeID ? '.workspace-main' : '#connections-heading')?.focus();
    focusAfterTransition.current = false;
  }, [activeID, modal]);

  function open(id: string) { setActiveID(id); setSection('overview'); }
  function edit(id: string) { setModal({ kind: 'connect', connection: state.connections.find(connection => connection.id === id) }); }
  function remove(id: string) {
    const connection = state.connections.find(connection => connection.id === id);
    if (connection) setModal({ kind: 'remove', connection });
  }
  const storageNotice = !state.storageAvailable && <p className="notice" role="alert">{t($ => $.connections.storageError)}</p>;

  return <>
    {active ? <WorkspaceLayout connection={active} section={section} sections={['overview', 'missions', 'agents', 'actions', 'settings']} onNavigate={setSection} onBack={() => setActiveID(undefined)}>
      {storageNotice}
      {section === 'settings' ? <ConnectionSettingsPage connection={active} signedIn={Boolean(state.credentials[active.id])}
        onEdit={() => edit(active.id)} onForget={() => state.forgetCredentials(active.id)} onRemove={() => remove(active.id)} />
        : section === 'missions' ? <MissionsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} />
          : section === 'agents' ? <AgentsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} />
            : section === 'actions' ? <ActionsPage key={`${active.id}:${active.url}`} connection={active} credentials={state.credentials[active.id]} onSignIn={() => edit(active.id)} />
              : <OverviewPage check={state.checks[active.id]} refreshing={state.refreshing} onRefresh={state.refresh} onSignIn={() => edit(active.id)} />}
    </WorkspaceLayout> : <ConnectionsPage connections={state.connections} statuses={Object.fromEntries(Object.entries(state.checks).map(([id, check]) => [id, check.status]))}
      onOpen={open} onAdd={() => setModal({ kind: 'connect' })} onEdit={edit} onRemove={remove}>
      {storageNotice}
      {local === 'checking' && <p className="local-discovery" role="status"><LoaderCircle className="connection-form-spinner" size={14} aria-hidden="true" />{t($ => $.connections.localChecking)}</p>}
      {local === 'found' && <div className="local-discovery"><div><span className="status-dot" aria-hidden="true" />{t($ => $.connections.localFound)}<span className="local-url">{LOCAL_API_URL}</span></div><Button variant="secondary" onClick={() => setModal({ kind: 'connect' })}>{t($ => $.connections.localConnect)}</Button></div>}
    </ConnectionsPage>}
    {modal?.kind === 'connect' && <ConnectionDialog connection={modal.connection} credentials={modal.connection ? state.credentials[modal.connection.id] : undefined}
      onClose={() => setModal(undefined)} onConnect={async (values, signal) => {
        const url = normalizeURL(values.url);
        if (state.connections.some(connection => connection.url === url && connection.id !== modal.connection?.id)) return t($ => $.connections.duplicate);
        const credentials = { username: values.username, password: values.password };
        const check = await checkConnection(url, credentials, signal);
        if (signal.aborted) return;
        if (check.status === 'authentication') return t($ => $.connections.authenticationError);
        if (check.status === 'degraded') return t($ => $.connections.degradedError);
        if (check.status !== 'ready') return t($ => $.connections.unreachableError);
        const id = modal.connection?.id ?? Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('');
        state.save({ id, name: values.name, url }, credentials, check);
        focusAfterTransition.current = true;
        setModal(undefined);
        if (activeID !== id || !['missions', 'agents', 'actions'].includes(section)) open(id);
      }} />}
    {modal?.kind === 'remove' && <Dialog open title={t($ => $.connections.removeTitle)} description={t($ => $.connections.removeDescription, { name: modal.connection.name })} onClose={() => setModal(undefined)}>
      <div className="dialog-actions">
        <Button variant="secondary" onClick={() => setModal(undefined)}>{t($ => $.connectionForm.cancel)}</Button>
        <Button className="danger-button" onClick={() => {
          focusAfterTransition.current = true;
          state.remove(modal.connection.id);
          if (activeID === modal.connection.id) setActiveID(undefined);
          setModal(undefined);
        }}>{t($ => $.connections.removeConfirm)}</Button>
      </div>
    </Dialog>}
  </>;
}

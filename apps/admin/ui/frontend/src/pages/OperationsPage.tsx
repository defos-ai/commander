import { useEffect, useRef, useState } from 'react';
import { ArrowLeft, ArrowUpRight, LoaderCircle, Plus, RefreshCw, Search, Workflow } from 'lucide-react';
import { getOperation, listOperations } from '../api/operations';
import type { Operation, OperationQuestion } from '../api/operation-types';
import { isActiveOperation, OperationDetails, OperationSecretsNotice, OperationStatus, type SessionNavigationTarget } from '../components/OperationDetails';
import { StartOperationDialog } from '../components/StartOperationDialog';
import { OperationCommandDialog } from '../components/OperationCommandDialog';
import { OperationGraphPanel } from '../components/OperationGraphPanel';
import { ArtifactDialog } from '../components/ArtifactDialog';
import { ReportDialog } from '../components/ReportDialog';
import { Button } from '../components/Button';
import type { Connection, Session } from '../connections/types';
import { useCatalogQuery } from '../hooks/useCatalogQuery';
import { useTranslation } from '../i18n';
import type { OperationView } from '../navigation/routes';
import '../styles/catalog.css';
import '../styles/operations.css';

const knownStatuses = ['planning', 'running', 'waiting', 'completed', 'failed', 'cancelled'] as const;
type CommandModal = { url: string; credentials: Session } & (
  { kind: 'start' } | { kind: 'cancel'; operation: Operation } | { kind: 'answer'; operation: Operation; question: OperationQuestion }
);
type ResourceModal = { url: string; credentials: Session; operationID: string } & (
  { kind: 'artifact'; artifactID: string; fileID?: string } | { kind: 'report' }
);

export function OperationsPage({ connection, credentials, onSignIn, selectedID: routeID, onSelect, view: routeView, onViewChange, onManageSecrets, onOpenSession }: {
  connection: Connection;
  credentials?: Session;
  onSignIn: () => void;
  selectedID?: string;
  onSelect?: (id?: string) => void;
  view?: OperationView;
  onViewChange?: (view: OperationView) => void;
  onManageSecrets?: () => void;
  onOpenSession?: (target: SessionNavigationTarget) => void;
}) {
  const { t } = useTranslation();
  const [localID, setLocalID] = useState<string>();
  const selectedID = onSelect ? routeID : localID;
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState('');
  const [localView, setLocalView] = useState<OperationView>('details');
  const view = onViewChange ? routeView ?? 'details' : localView;
  const [modal, setModal] = useState<CommandModal>();
  const [resource, setResource] = useState<ResourceModal>();
  const [notice, setNotice] = useState<{ kind: 'created' | 'cancelled' | 'answered'; operationID: string }>();
  const heading = useRef<HTMLHeadingElement>(null);
  const focusHeading = useRef(false);
  const query = useCatalogQuery<Operation>({ url: connection.url, credentials, id: selectedID, list: listOperations, get: getOperation, pollInterval: 10_000 });
  const items = query.items ?? [];
  const filter = search.trim().toLocaleLowerCase();
  const statuses = [...new Set([...knownStatuses, ...items.map(item => item.status), ...(status ? [status] : [])])];
  const visible = items.filter(item => (!status || item.status === status) &&
    `${item.id} ${item.mission.name}`.toLocaleLowerCase().includes(filter));
  const error = !credentials ? 'authentication' : query.error?.kind;
  const currentModal = credentials && modal?.url === connection.url && modal.credentials === credentials &&
    (modal.kind === 'start' ? selectedID === undefined : modal.operation.id === selectedID) ? modal : undefined;
  const currentResource = credentials && resource?.url === connection.url && resource.credentials === credentials && resource.operationID === selectedID ? resource : undefined;
  const commandOperation = currentModal && currentModal.kind !== 'start' ? query.item ?? currentModal.operation : undefined;
  const commandQuestion = currentModal?.kind === 'answer' ? commandOperation?.questions?.find(question => question.id === currentModal.question.id) : undefined;

  useEffect(() => {
    if (focusHeading.current) { heading.current?.focus(); focusHeading.current = false; }
  }, [selectedID]);

  useEffect(() => { setModal(undefined); setResource(undefined); }, [selectedID, view]);

  function select(id?: string) {
    focusHeading.current = true;
    if (onSelect) onSelect(id); else setLocalID(id);
    setNotice(undefined); setLocalView('details'); setResource(undefined);
  }
  function changeView(next: OperationView) { if (onViewChange) onViewChange(next); else setLocalView(next); }
  function clearFilters() { setSearch(''); setStatus(''); }
  function inspect(id: string) { setModal(undefined); select(id); query.refresh(); }
  function openArtifact(artifactID: string, fileID?: string) {
    if (credentials && selectedID) setResource({ kind: 'artifact', artifactID, fileID, operationID: selectedID, url: connection.url, credentials });
  }
  function openReport() {
    if (credentials && selectedID) setResource({ kind: 'report', operationID: selectedID, url: connection.url, credentials });
  }

  return <section className={`catalog-page operations-page${view === 'graph' ? ' graph-view' : ''}`}>
    {selectedID !== undefined && <button className="catalog-back" type="button" onClick={() => select()}>
      <ArrowLeft size={16} aria-hidden="true" />{t($ => $.operations.back)}
    </button>}
    <div className="page-heading">
      <div><h1 ref={heading} tabIndex={-1}>{selectedID ?? t($ => $.operations.title)}</h1>
        {selectedID === undefined && <p>{t($ => $.operations.description)}</p>}
      </div>
      <div className="operation-controls">{credentials && view !== 'graph' && <Button variant="secondary" onClick={query.refresh} disabled={query.refreshing}>
        <RefreshCw size={15} aria-hidden="true" />{query.refreshing ? t($ => $.operations.refreshing) : t($ => $.operations.refresh)}
      </Button>}
        {credentials && !error && selectedID === undefined && <Button onClick={() => setModal({ kind: 'start', url: connection.url, credentials })}>
          <Plus size={15} aria-hidden="true" />{t($ => $.operationCommands.startTitle)}
        </Button>}
        {credentials && query.item && isActiveOperation(query.item.status) && <Button variant="secondary" className="danger-button"
          onClick={() => setModal({ kind: 'cancel', operation: query.item!, url: connection.url, credentials })}>{t($ => $.operationCommands.cancelTitle)}</Button>}
      </div>
    </div>
    {notice && notice.operationID === selectedID && <p className="notice" role="status">{t($ => $.operationCommands.notices[notice.kind])}</p>}
    {error ? <div className="notice" role="alert">
      <p>{t($ => $.operations.errors[error])}</p>
      {error === 'authentication' ? <Button onClick={onSignIn}>{t($ => $.operations.signIn)}</Button>
        : error !== 'not_found' && <Button variant="secondary" onClick={query.refresh}>{t($ => $.operations.retry)}</Button>}
    </div> : query.loading ? <p className="catalog-loading" role="status">
      <LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />
      {selectedID === undefined ? t($ => $.operations.loading) : t($ => $.operations.loadingDetail)}
    </p> : query.item ? <>
      <div className="operation-view-switch" role="group" aria-label={t($ => $.operationGraph.viewLabel)}>
        <button type="button" aria-pressed={view === 'details'} onClick={() => changeView('details')}>{t($ => $.operationGraph.details)}</button>
        <button type="button" aria-pressed={view === 'graph'} onClick={() => changeView('graph')}>{t($ => $.operationGraph.title)}</button>
      </div>
      <OperationSecretsNotice operation={query.item} onManageSecrets={onManageSecrets} />
      {view === 'graph' ? <OperationGraphPanel url={connection.url} credentials={credentials} operationID={query.item.id} tasks={query.item.tasks}
        placements={query.item.preflight?.tasks} onSignIn={onSignIn} onOpenArtifact={openArtifact} onOpenSession={onOpenSession} />
        : <OperationDetails operation={query.item} onOpenArtifact={openArtifact} onOpenReport={openReport} onOpenSession={onOpenSession}
          onAnswer={credentials ? question => setModal({ kind: 'answer', operation: query.item!, question, url: connection.url, credentials }) : undefined} />}
    </> : <>
      {(items.length > 0 || filter || status) && <div className="catalog-toolbar">
        <div className="operation-filters">
          <label className="catalog-search"><Search size={17} aria-hidden="true" />
            <input type="search" aria-label={t($ => $.operations.search)} placeholder={t($ => $.operations.searchPlaceholder)} value={search} onChange={event => setSearch(event.target.value)} />
          </label>
          <select aria-label={t($ => $.operations.statusFilter)} value={status} onChange={event => setStatus(event.target.value)}>
            <option value="">{t($ => $.operations.allStatuses)}</option>
            {statuses.map(value => <option key={value} value={value}>{(knownStatuses as readonly string[]).includes(value)
              ? t($ => $.operations.statuses[value as typeof knownStatuses[number]]) : value}</option>)}
          </select>
        </div>
        <p role="status">{filter || status ? t($ => $.operations.filtered, { count: visible.length, total: items.length }) : t($ => $.operations.count, { count: items.length })}</p>
      </div>}
      {visible.length > 0 ? <ul className="catalog-list">{visible.map(item => <li key={item.id}>
        <button type="button" className="catalog-row" aria-label={t($ => $.operations.open, { id: item.id })} onClick={() => select(item.id)}>
          <span className="catalog-row-main"><span className="catalog-row-name">{item.mission.name}</span>
            <span className="operation-id">{item.id}</span>
            <span className="catalog-row-meta"><OperationStatus status={item.status} />
              <span>{t($ => $.operations.progress, { total: item.progress.total_tasks, finished: item.progress.finished_tasks })}</span>
              <time dateTime={item.updated_at}>{t($ => $.operations.updated, { time: new Date(item.updated_at).toLocaleString() })}</time>
            </span>
            {item.error && <span className="catalog-row-description">{item.error}</span>}
          </span><ArrowUpRight size={18} aria-hidden="true" />
        </button>
      </li>)}</ul> : <div className="catalog-empty">
        <Workflow size={26} strokeWidth={1.3} aria-hidden="true" /><h2>{filter || status ? t($ => $.operations.noMatches) : t($ => $.operations.emptyTitle)}</h2>
        {filter || status ? <Button variant="secondary" onClick={clearFilters}>{t($ => $.operations.clearSearch)}</Button> : <p>{t($ => $.operations.emptyDescription)}</p>}
      </div>}
    </>}
    {credentials && !query.loading && !error && view !== 'graph' && <p className="catalog-updated">{t($ => $.operations.polling)} {query.checkedAt !== undefined && t($ => $.operations.checked, { time: new Date(query.checkedAt).toLocaleTimeString() })}</p>}
    {currentResource?.kind === 'artifact' && <ArtifactDialog key={`${currentResource.url}:${currentResource.artifactID}:${currentResource.fileID ?? ''}`}
      url={currentResource.url} credentials={currentResource.credentials} artifactID={currentResource.artifactID} initialFileID={currentResource.fileID} onClose={() => setResource(undefined)} />}
    {currentResource?.kind === 'report' && <ReportDialog key={`${currentResource.url}:${currentResource.operationID}`}
      url={currentResource.url} credentials={currentResource.credentials} operationID={currentResource.operationID} onClose={() => setResource(undefined)} />}
    {currentModal?.kind === 'start' && <StartOperationDialog connection={connection} credentials={currentModal.credentials}
      onClose={() => setModal(undefined)} onInspect={inspect} onStarted={operation => {
        setModal(undefined); select(operation.id); setNotice({ kind: 'created', operationID: operation.id });
      }} />}
    {currentModal && currentModal.kind !== 'start' && commandOperation && <OperationCommandDialog
      {...(currentModal.kind === 'answer' ? { command: 'answer' as const, question: commandQuestion ?? currentModal.question } : { command: 'cancel' as const })}
      operation={commandOperation} connection={connection} credentials={currentModal.credentials}
      available={!error && (currentModal.kind !== 'answer' || Boolean(commandQuestion))}
      onClose={() => setModal(undefined)} onInspect={inspect} onCompleted={() => {
        setNotice({ kind: currentModal.kind === 'cancel' ? 'cancelled' : 'answered', operationID: currentModal.operation.id });
        setModal(undefined); query.refresh();
      }} />}
  </section>;
}

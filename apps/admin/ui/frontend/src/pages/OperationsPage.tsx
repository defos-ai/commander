import { useEffect, useRef, useState } from 'react';
import { ArrowLeft, ArrowUpRight, LoaderCircle, Plus, RefreshCw, Search, Workflow } from 'lucide-react';
import { getOperation, listOperations } from '../api/operations';
import type { Operation, OperationQuestion } from '../api/operation-types';
import type { OperationLogFilters } from '../api/operation-logs';
import { isActiveOperation, OperationDetails, OperationSecretsNotice, OperationStatus, type SessionNavigationTarget } from '../components/OperationDetails';
import { StartOperationDialog } from '../components/StartOperationDialog';
import { OperationCommandDialog } from '../components/OperationCommandDialog';
import { CompleteOperationDialog } from '../components/CompleteOperationDialog';
import { OperationGraphPanel } from '../components/OperationGraphPanel';
import { OperationLogsPanel } from '../components/OperationLogsPanel';
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
const emptyLogFilters: OperationLogFilters = {};
type CommandModal = { url: string; credentials: Session } & (
  { kind: 'start' } | { kind: 'cancel'; operation: Operation } | { kind: 'complete'; operation: Operation; connectionID: string } |
  { kind: 'answer'; operation: Operation; question: OperationQuestion }
);
type ResourceModal = { url: string; credentials: Session; operationID: string } & (
  { kind: 'artifact'; artifactID: string; fileID?: string } | { kind: 'report' }
);

export function OperationsPage({ connection, credentials, onSignIn, selectedID: routeID, onSelect, view: routeView, onViewChange,
  logFilters: routeLogFilters, onLogFiltersChange, onManageSecrets, onOpenSession }: {
  connection: Connection;
  credentials?: Session;
  onSignIn: () => void;
  selectedID?: string;
  onSelect?: (id?: string) => void;
  view?: OperationView;
  onViewChange?: (view: OperationView, logFilters?: OperationLogFilters) => void;
  logFilters?: OperationLogFilters;
  onLogFiltersChange?: (filters: OperationLogFilters) => void;
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
  const [rememberedLogs, setRememberedLogs] = useState({ operationID: selectedID, filters: routeLogFilters ?? emptyLogFilters });
  const localLogFilters = rememberedLogs.operationID === selectedID ? rememberedLogs.filters : emptyLogFilters;
  const controlledLogFilters = Boolean(onLogFiltersChange);
  const logFilters = controlledLogFilters && view === 'logs' ? routeLogFilters ?? emptyLogFilters : localLogFilters;
  const [modal, setModal] = useState<CommandModal>();
  const [resource, setResource] = useState<ResourceModal>();
  const [notice, setNotice] = useState<{ kind: 'created' | 'cancelled' | 'answered' | 'completed'; operationID: string }>();
  const [submittedCompletions, setSubmittedCompletions] = useState<ReadonlySet<string>>(() => new Set());
  const [completionReadback, setCompletionReadback] = useState<{ key: string; busy: boolean; failed: boolean }>();
  const pendingReadback = useRef<AbortController | null>(null);
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
    (modal.kind !== 'complete' || modal.connectionID === connection.id) &&
    (modal.kind === 'start' ? selectedID === undefined : modal.operation.id === selectedID) ? modal : undefined;
  const currentResource = credentials && resource?.url === connection.url && resource.credentials === credentials && resource.operationID === selectedID ? resource : undefined;
  const commandOperation = currentModal && currentModal.kind !== 'start' ? query.item ?? currentModal.operation : undefined;
  const commandQuestion = currentModal?.kind === 'answer' ? commandOperation?.questions?.find(question => question.id === currentModal.question.id) : undefined;
  const completionKey = `${connection.id}\0${connection.url}\0${selectedID ?? ''}`;
  const completionSubmitted = submittedCompletions.has(completionKey);
  const inspectingCompletion = completionReadback?.key === completionKey && completionReadback.busy;
  const completionReadbackFailed = completionReadback?.key === completionKey && completionReadback.failed;

  useEffect(() => {
    if (focusHeading.current) { heading.current?.focus(); focusHeading.current = false; }
  }, [selectedID]);

  useEffect(() => { setModal(undefined); setResource(undefined); }, [selectedID, view, connection.id, connection.url, credentials]);
  useEffect(() => { setNotice(undefined); }, [connection.id, connection.url, credentials]);
  useEffect(() => {
    setCompletionReadback(undefined);
    return () => { pendingReadback.current?.abort(); pendingReadback.current = null; };
  }, [connection.id, connection.url, credentials, selectedID, view]);
  useEffect(() => {
    if (view === 'logs' && controlledLogFilters) setRememberedLogs({ operationID: selectedID, filters: routeLogFilters ?? emptyLogFilters });
  }, [selectedID, view, controlledLogFilters, routeLogFilters]);

  function select(id?: string) {
    focusHeading.current = true;
    if (onSelect) onSelect(id); else setLocalID(id);
    setNotice(undefined); setLocalView('details'); setResource(undefined); setRememberedLogs({ operationID: id, filters: emptyLogFilters });
  }
  function changeView(next: OperationView, filters = logFilters) {
    setRememberedLogs({ operationID: selectedID, filters });
    if (onViewChange) onViewChange(next, filters);
    else {
      setLocalView(next);
      if (next === 'logs' && onLogFiltersChange) onLogFiltersChange(filters);
    }
  }
  function changeLogFilters(filters: OperationLogFilters) {
    setRememberedLogs({ operationID: selectedID, filters });
    onLogFiltersChange?.(filters);
  }
  function clearFilters() { setSearch(''); setStatus(''); }
  function inspect(id: string) { setModal(undefined); select(id); query.refresh(); }
  function markCompletion(submitted: boolean) {
    setSubmittedCompletions(previous => {
      const next = new Set(previous);
      if (submitted) next.add(completionKey); else next.delete(completionKey);
      return next;
    });
  }
  async function inspectCompletion(id: string) {
    if (!credentials || pendingReadback.current) return;
    const controller = new AbortController();
    pendingReadback.current = controller;
    setCompletionReadback({ key: completionKey, busy: true, failed: false });
    try {
      await getOperation(connection.url, credentials, id, controller.signal);
      if (!controller.signal.aborted && pendingReadback.current === controller) {
        markCompletion(false);
        setCompletionReadback(undefined);
        inspect(id);
      }
    } catch {
      if (!controller.signal.aborted && pendingReadback.current === controller) {
        setCompletionReadback({ key: completionKey, busy: false, failed: true });
      }
    } finally {
      if (pendingReadback.current === controller) pendingReadback.current = null;
    }
  }
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
      <div className="operation-controls">{credentials && view === 'details' && <Button variant="secondary" onClick={query.refresh} disabled={query.refreshing}>
        <RefreshCw size={15} aria-hidden="true" />{query.refreshing ? t($ => $.operations.refreshing) : t($ => $.operations.refresh)}
      </Button>}
        {credentials && !error && selectedID === undefined && <Button onClick={() => setModal({ kind: 'start', url: connection.url, credentials })}>
          <Plus size={15} aria-hidden="true" />{t($ => $.operationCommands.startTitle)}
        </Button>}
        {credentials && query.item && isActiveOperation(query.item.status) && <Button variant="secondary" className="danger-button"
          onClick={() => setModal({ kind: 'cancel', operation: query.item!, url: connection.url, credentials })}>{t($ => $.operationCommands.cancelTitle)}</Button>}
        {credentials && query.item && !error && isActiveOperation(query.item.status) && <Button disabled={completionSubmitted}
          onClick={() => setModal({ kind: 'complete', operation: query.item!, url: connection.url, credentials, connectionID: connection.id })}>{t($ => $.operationCommands.completeTitle)}</Button>}
      </div>
    </div>
    {notice && notice.operationID === selectedID && <p className="notice" role="status">{t($ => $.operationCommands.notices[notice.kind])}</p>}
    {credentials && completionSubmitted && currentModal?.kind !== 'complete' && <div className="notice" role="status">
      <p>{t($ => $.operationCommands.completionSubmitted)}</p>
      {completionReadbackFailed && <p role="alert">{t($ => $.operationCommands.completionReadbackFailed)}</p>}
      <Button variant="secondary" disabled={inspectingCompletion} onClick={() => { if (selectedID) void inspectCompletion(selectedID); }}>
        {t($ => $.operationCommands.viewOperation)}
      </Button>
    </div>}
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
        <button type="button" aria-pressed={view === 'logs'} onClick={() => changeView('logs')}>{t($ => $.operationLogs.title)}</button>
      </div>
      <OperationSecretsNotice operation={query.item} onManageSecrets={onManageSecrets} />
      {view === 'graph' ? <OperationGraphPanel url={connection.url} credentials={credentials} operationID={query.item.id} tasks={query.item.tasks}
        placements={query.item.preflight?.tasks} onSignIn={onSignIn} onOpenArtifact={openArtifact} onOpenSession={onOpenSession} />
        : view === 'logs' ? <OperationLogsPanel url={connection.url} credentials={credentials} operationID={query.item.id} tasks={query.item.tasks ?? []}
          filters={logFilters} onFiltersChange={changeLogFilters} onSignIn={onSignIn} />
        : <OperationDetails operation={query.item} onOpenArtifact={openArtifact} onOpenReport={openReport} onOpenSession={onOpenSession}
          onOpenLogs={taskID => changeView('logs', { task_id: taskID })}
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
    {credentials && !query.loading && !error && view === 'details' && <p className="catalog-updated">{t($ => $.operations.polling)} {query.checkedAt !== undefined && t($ => $.operations.checked, { time: new Date(query.checkedAt).toLocaleTimeString() })}</p>}
    {currentResource?.kind === 'artifact' && <ArtifactDialog key={`${currentResource.url}:${currentResource.artifactID}:${currentResource.fileID ?? ''}`}
      url={currentResource.url} credentials={currentResource.credentials} artifactID={currentResource.artifactID} initialFileID={currentResource.fileID} onClose={() => setResource(undefined)} />}
    {currentResource?.kind === 'report' && <ReportDialog key={`${currentResource.url}:${currentResource.operationID}`}
      url={currentResource.url} credentials={currentResource.credentials} operationID={currentResource.operationID} onClose={() => setResource(undefined)} />}
    {currentModal?.kind === 'start' && <StartOperationDialog connection={connection} credentials={currentModal.credentials}
      onClose={() => setModal(undefined)} onInspect={inspect} onStarted={operation => {
        setModal(undefined); select(operation.id); setNotice({ kind: 'created', operationID: operation.id });
      }} />}
    {currentModal?.kind === 'complete' && commandOperation && <CompleteOperationDialog operation={commandOperation}
      connection={connection} credentials={currentModal.credentials} available={!error && Boolean(query.item)}
      inspecting={inspectingCompletion} readbackFailed={completionReadbackFailed}
      onClose={() => setModal(undefined)} onInspect={id => { void inspectCompletion(id); }}
      onSubmitted={() => markCompletion(true)} onRejected={() => markCompletion(false)} onCompleted={() => {
        markCompletion(false); setNotice({ kind: 'completed', operationID: currentModal.operation.id });
        setModal(undefined); query.refresh();
      }} />}
    {currentModal && currentModal.kind !== 'start' && currentModal.kind !== 'complete' && commandOperation && <OperationCommandDialog
      {...(currentModal.kind === 'answer' ? { command: 'answer' as const, question: commandQuestion ?? currentModal.question } : { command: 'cancel' as const })}
      operation={commandOperation} connection={connection} credentials={currentModal.credentials}
      available={!error && (currentModal.kind !== 'answer' || Boolean(commandQuestion))}
      onClose={() => setModal(undefined)} onInspect={inspect} onCompleted={() => {
        setNotice({ kind: currentModal.kind === 'cancel' ? 'cancelled' : 'answered', operationID: currentModal.operation.id });
        setModal(undefined); query.refresh();
      }} />}
  </section>;
}

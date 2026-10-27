import { useEffect, useId, useMemo, useRef, useState, type FormEvent } from 'react';
import { ArrowLeft, ArrowUpRight, Boxes, LoaderCircle, RefreshCw, Search } from 'lucide-react';
import { ApiError } from '../api/client';
import { getRuntimeSession, listRuntimeSessions, type RuntimeSession, type SessionFilters, type SessionControlMethod } from '../api/runtime-sessions';
import { Button } from '../components/Button';
import { SessionControlDialog, canControlSession } from '../components/SessionControlDialog';
import { SessionDetails, SessionDisposition, SessionState } from '../components/SessionDetails';
import type { Connection, Session } from '../connections/types';
import { useCatalogQuery } from '../hooks/useCatalogQuery';
import { useTranslation } from '../i18n';
import '../styles/catalog.css';
import '../styles/sessions.css';

type Props = {
  connection: Connection; credentials?: Session; onSignIn: () => void;
  selectedID?: string; onSelect?: (id?: string) => void; onOpenAgent?: (id: string) => void;
  filters?: SessionFilters; onFiltersChange?: (filters: SessionFilters) => void;
};
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const agentID = /^[a-z0-9]+(?:-[a-z0-9]+)*-agent-[0-9a-f]{8}$/;
const emptyFilters: SessionFilters = {};
type ControlRequest = { session: RuntimeSession; requestID: string };
type ControlGuard = { current: Map<string, ControlRequest>; register: (key: string, request: ControlRequest) => void; clear: (key: string, requestID: string) => void };

export function SessionsPage(props: Props) {
  const requests = useRef(new Map<string, ControlRequest>());
  const [, render] = useState(0);
  const guard: ControlGuard = {
    current: requests.current,
    register(key, request) { requests.current.set(key, request); render(value => value + 1); },
    clear(key, requestID) { if (requests.current.get(key)?.requestID === requestID) { requests.current.delete(key); render(value => value + 1); } },
  };
  return <SessionsContent key={`${props.connection.id}\0${props.connection.url}`} {...props} guard={guard} />;
}

function SessionsContent({ connection, credentials, onSignIn, selectedID: routeID, onSelect, onOpenAgent, filters: routeFilters, onFiltersChange, guard }: Props & { guard: ControlGuard }) {
  const { t } = useTranslation();
  const identity = useId();
  const [localID, setLocalID] = useState<string>();
  const selectedID = onSelect ? routeID : localID;
  const [scopeInput, setScopeInput] = useState(routeFilters?.scope_id ?? '');
  const [ownerInput, setOwnerInput] = useState(routeFilters?.owner_agent_id ?? '');
  const [localFilters, setLocalFilters] = useState<SessionFilters>(routeFilters ?? emptyFilters);
  const filters = onFiltersChange ? routeFilters ?? emptyFilters : localFilters;
  const [invalidFilter, setInvalidFilter] = useState(false);
  const [search, setSearch] = useState('');
  const [activity, setActivity] = useState('all');
  const [dialog, setDialog] = useState<{ credentials: Session; session: RuntimeSession; method: SessionControlMethod }>();
  const [notice, setNotice] = useState<string>();
  const [readingSaved, setReadingSaved] = useState(false);
  const [readError, setReadError] = useState<ApiError>();
  const readRequest = useRef<AbortController | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const focusHeading = useRef(false);
  const list = useMemo(() => (url: string, session: Session, signal?: AbortSignal) => listRuntimeSessions(url, session, filters, signal), [filters.scope_id, filters.owner_agent_id]);
  const query = useCatalogQuery<RuntimeSession>({ url: connection.url, credentials, id: selectedID, list, get: getRuntimeSession, pollInterval: 10_000 });
  const items = query.items ?? [];
  const filter = search.trim().toLocaleLowerCase();
  const visible = items.filter(item => `${item.label} ${item.id} ${item.scope_id} ${item.owner_agent_id} ${item.hostname ?? ''} ${item.peer ?? ''} ${item.state} ${item.disposition ?? ''}`.toLocaleLowerCase().includes(filter)
    && (activity === 'all' || item.busy === (activity === 'busy')));
  const error = !credentials ? 'authentication' : query.error?.kind;
  const controlKey = `${connection.id}\0${connection.url}\0${selectedID}`;
  const outstanding = guard.current.get(controlKey);
  const dialogVisible = Boolean(credentials && dialog?.credentials === credentials && dialog.session.id === selectedID);

  useEffect(() => {
    if (focusHeading.current) { heading.current?.focus(); focusHeading.current = false; }
  }, [selectedID]);
  useEffect(() => { setDialog(undefined); setNotice(undefined); }, [credentials, selectedID]);
  useEffect(() => {
    setReadingSaved(false); setReadError(undefined);
    return () => { readRequest.current?.abort(); readRequest.current = null; };
  }, [credentials, selectedID]);
  useEffect(() => { setScopeInput(filters.scope_id ?? ''); setOwnerInput(filters.owner_agent_id ?? ''); setInvalidFilter(false); }, [filters.scope_id, filters.owner_agent_id]);

  function setFilters(next: SessionFilters) { if (onFiltersChange) onFiltersChange(next); else setLocalFilters(next); }

  function select(id?: string) { setDialog(undefined); setNotice(undefined); focusHeading.current = true; if (onSelect) onSelect(id); else setLocalID(id); }

  function applyFilters(event: FormEvent) {
    event.preventDefault();
    const scope = scopeInput.trim();
    const owner = ownerInput.trim();
    if (scope && !uuid.test(scope) || owner && (!agentID.test(owner) || owner.length > 63)) { setInvalidFilter(true); return; }
    setInvalidFilter(false);
    setFilters({ ...(scope ? { scope_id: scope } : {}), ...(owner ? { owner_agent_id: owner } : {}) });
  }

  function openControl(method: SessionControlMethod) {
    if (!credentials || !query.item || query.error || guard.current.has(controlKey) || !canControlSession(query.item, method)) return;
    setNotice(undefined);
    setDialog({ credentials, session: query.item, method });
  }

  async function inspectSaved() {
    if (!credentials || !outstanding || readRequest.current) return;
    const controller = new AbortController();
    readRequest.current = controller;
    setReadingSaved(true); setReadError(undefined);
    try {
      const result = await getRuntimeSession(connection.url, credentials, outstanding.session.id, controller.signal);
      if (result.scope_id !== outstanding.session.scope_id || result.owner_agent_id !== outstanding.session.owner_agent_id) throw new ApiError('invalid_response');
      if (!controller.signal.aborted && readRequest.current === controller) {
        guard.clear(controlKey, outstanding.requestID);
        setNotice(t($ => $.runtimeSessions.savedState, { state: result.state, disposition: result.disposition }));
        query.refresh();
      }
    } catch (cause) {
      if (!controller.signal.aborted && readRequest.current === controller) setReadError(cause instanceof ApiError ? cause : new ApiError('unavailable'));
    } finally {
      if (!controller.signal.aborted && readRequest.current === controller) { readRequest.current = null; setReadingSaved(false); }
    }
  }

  return <section className="catalog-page sessions-page">
    {selectedID !== undefined && <button className="catalog-back" type="button" onClick={() => select()}><ArrowLeft size={16} aria-hidden="true" />{t($ => $.runtimeSessions.back)}</button>}
    <div className="page-heading"><div><h1 ref={heading} tabIndex={-1}>{t($ => $.runtimeSessions.title)}</h1><p>{t($ => $.runtimeSessions.description)}</p></div>
      {credentials && <Button variant="secondary" onClick={query.refresh} disabled={query.loading || query.refreshing}><RefreshCw size={15} aria-hidden="true" />{query.refreshing ? t($ => $.runtimeSessions.refreshing) : t($ => $.runtimeSessions.refresh)}</Button>}
    </div>
    {notice && <p className="notice" role="status">{notice}</p>}
    {credentials && outstanding && !dialogVisible && <div className="notice session-control-guard" role="status">
      <p>{t($ => $.runtimeSessions.readbackRequired)}</p><p className="session-request-id">{t($ => $.runtimeSessions.requestID)}: <code>{outstanding.requestID}</code></p>
      <Button variant="secondary" disabled={readingSaved} onClick={() => void inspectSaved()}>{readingSaved ? t($ => $.runtimeSessions.readingSaved) : t($ => $.runtimeSessions.inspectSaved)}</Button>
      {readError && <p role="alert">{t($ => $.runtimeSessions.errors[readError.kind])}</p>}
    </div>}
    {credentials && selectedID === undefined && <form className="session-filters" aria-label={t($ => $.runtimeSessions.filters)} onSubmit={applyFilters}>
      <div className="form-field"><label htmlFor={`${identity}-scope`}>{t($ => $.runtimeSessions.scopeFilter)}</label><input id={`${identity}-scope`} value={scopeInput} onChange={event => setScopeInput(event.target.value)} autoCapitalize="none" spellCheck={false} /></div>
      <div className="form-field"><label htmlFor={`${identity}-owner`}>{t($ => $.runtimeSessions.ownerFilter)}</label><input id={`${identity}-owner`} value={ownerInput} onChange={event => setOwnerInput(event.target.value)} autoCapitalize="none" spellCheck={false} /></div>
      <div className="session-filter-actions"><Button type="submit" variant="secondary" disabled={query.refreshing}>{t($ => $.runtimeSessions.applyFilters)}</Button>
        {(scopeInput || ownerInput || filters.scope_id || filters.owner_agent_id) && <Button variant="secondary" onClick={() => { setScopeInput(''); setOwnerInput(''); setFilters({}); setInvalidFilter(false); }}>{t($ => $.runtimeSessions.clearFilters)}</Button>}
      </div><p className="form-hint">{t($ => $.runtimeSessions.filterHint)}</p>
      {invalidFilter && <p className="notice" role="alert">{t($ => $.runtimeSessions.invalidFilter)}</p>}
    </form>}
    {error ? <div className="notice" role="alert"><p>{t($ => $.runtimeSessions.errors[error])}</p>
      {error === 'authentication' ? <Button onClick={onSignIn}>{t($ => $.runtimeSessions.signIn)}</Button>
        : error !== 'forbidden' && error !== 'not_found' && <Button variant="secondary" onClick={query.refresh}>{t($ => $.runtimeSessions.retry)}</Button>}
    </div> : query.item ? <>
      <SessionDetails session={query.item} onOpenAgent={onOpenAgent} />
      <section className="catalog-section session-controls"><h2>{t($ => $.runtimeSessions.controls)}</h2><p className="catalog-muted">{t($ => $.runtimeSessions.controlsDescription)}</p>
        {!query.item.owner_agent_id && <p className="form-hint">{t($ => $.runtimeSessions.noOwner)}</p>}
        <div className="session-control-actions">{(['inspect', 'verify', 'close', 'abandon'] as const).map(method => method === 'inspect' && outstanding ? null : <Button key={method} variant="secondary"
          className={method === 'close' || method === 'abandon' ? 'danger-button' : undefined}
          disabled={Boolean(outstanding) || !canControlSession(query.item!, method)} onClick={() => openControl(method)}>{t($ => $.runtimeSessions.methods[method])}</Button>)}</div>
      </section>
    </> : selectedID !== undefined ? <p className="catalog-loading" role="status"><LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />{t($ => $.runtimeSessions.loadingDetail)}</p> : <>
      {query.loading ? <p className="catalog-loading" role="status"><LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />{t($ => $.runtimeSessions.loading)}</p> : <>
        <div className="catalog-toolbar"><label className="catalog-search"><Search size={17} aria-hidden="true" /><input type="search" aria-label={t($ => $.runtimeSessions.search)} placeholder={t($ => $.runtimeSessions.searchPlaceholder)} value={search} onChange={event => setSearch(event.target.value)} /></label>
          <label className="session-activity-filter">{t($ => $.runtimeSessions.busyFilter)}<select value={activity} onChange={event => setActivity(event.target.value)}>
            <option value="all">{t($ => $.runtimeSessions.allActivity)}</option><option value="busy">{t($ => $.runtimeSessions.busyOnly)}</option><option value="idle">{t($ => $.runtimeSessions.idleOnly)}</option>
          </select></label>
          <p role="status">{filter || activity !== 'all' ? t($ => $.runtimeSessions.filtered, { count: visible.length, total: items.length }) : t($ => $.runtimeSessions.count, { count: items.length })}</p>
        </div>
        {visible.length ? <ul className="catalog-list">{visible.map(item => <li key={`${item.scope_id}:${item.id}`}><button type="button" className="catalog-row" aria-label={t($ => $.runtimeSessions.open, { id: item.id })} onClick={() => select(item.id)}>
          <span className="catalog-row-main"><span className="catalog-row-name">{item.label}</span><span className="catalog-row-description">{item.id}</span>
            <span className="catalog-row-meta"><SessionState state={item.state} /><SessionDisposition disposition={item.disposition} /><span>{item.busy ? t($ => $.runtimeSessions.busy) : t($ => $.runtimeSessions.idle)}</span></span>
            <span className="catalog-row-description">{t($ => $.runtimeSessions.owner)}: {item.owner_agent_id || t($ => $.runtimeSessions.notReported)}</span>
            <span className="catalog-row-description">{t($ => $.runtimeSessions.scope)}: {item.scope_id}</span>
          </span><ArrowUpRight size={18} aria-hidden="true" />
        </button></li>)}</ul> : <div className="catalog-empty"><Boxes size={26} strokeWidth={1.3} aria-hidden="true" />
          <h2>{items.length ? t($ => $.runtimeSessions.noMatches) : t($ => $.runtimeSessions.emptyTitle)}</h2>
          {items.length ? <Button variant="secondary" onClick={() => { setSearch(''); setActivity('all'); }}>{t($ => $.runtimeSessions.clearSearch)}</Button> : <p>{t($ => $.runtimeSessions.emptyDescription)}</p>}
        </div>}
      </>}
    </>}
    {credentials && !query.loading && !error && <p className="catalog-updated">{t($ => $.runtimeSessions.polling)} {query.checkedAt !== undefined && t($ => $.runtimeSessions.checked, { time: new Date(query.checkedAt).toLocaleTimeString() })}</p>}
    {credentials && dialog?.credentials === credentials && dialog.session.id === selectedID && <SessionControlDialog connection={connection} credentials={credentials} session={query.item ?? dialog.session} method={dialog.method}
      available={!query.error && !query.loading} onClose={() => setDialog(undefined)} onRefresh={query.refresh}
      onSubmitted={requestID => guard.register(controlKey, { session: dialog.session, requestID })}
      onReadBack={(_session, requestID) => guard.clear(controlKey, requestID)}
      onCompleted={(session, requestID) => { guard.clear(controlKey, requestID); setDialog(undefined); setNotice(session.disposition === 'closing' ? t($ => $.runtimeSessions.closePending) : t($ => $.runtimeSessions.completed)); query.refresh(); }} />}
  </section>;
}

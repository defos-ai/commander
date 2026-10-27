import { useEffect, useRef, useState } from 'react';
import { ArrowLeft, ArrowUpRight, Bot, KeyRound, LoaderCircle, RefreshCw, Search } from 'lucide-react';
import { getAgent, listAgents } from '../api/agents';
import type { Agent } from '../api/types';
import { AgentDetails, AgentStatus } from '../components/AgentDetails';
import { Button } from '../components/Button';
import type { Connection, Session } from '../connections/types';
import { useCatalogQuery } from '../hooks/useCatalogQuery';
import { useTranslation } from '../i18n';
import '../styles/catalog.css';

export function AgentsPage({ connection, credentials, onSignIn, selectedID: routeID, onSelect, onManageSecrets, onOpenSessions }: {
  connection: Connection;
  credentials?: Session;
  onSignIn: () => void;
  selectedID?: string;
  onSelect?: (id?: string) => void;
  onManageSecrets?: () => void;
  onOpenSessions?: (ownerAgentID?: string) => void;
}) {
  const { t } = useTranslation();
  const [localID, setLocalID] = useState<string>();
  const selectedID = onSelect ? routeID : localID;
  const [search, setSearch] = useState('');
  const heading = useRef<HTMLHeadingElement>(null);
  const focusHeading = useRef(false);
  const query = useCatalogQuery<Agent>({ url: connection.url, credentials, id: selectedID, list: listAgents, get: getAgent, pollInterval: 30_000 });
  const items = query.items ?? [];
  const filter = search.trim().toLocaleLowerCase();
  const visible = items.filter(item => `${item.id} ${item.type} ${item.server.hostname}`.toLocaleLowerCase().includes(filter));
  const error = !credentials ? 'authentication' : query.error?.kind;

  useEffect(() => {
    if (focusHeading.current) { heading.current?.focus(); focusHeading.current = false; }
  }, [selectedID]);

  function select(id?: string) { focusHeading.current = true; if (onSelect) onSelect(id); else setLocalID(id); }

  return <section className="catalog-page">
    {selectedID !== undefined && <button className="catalog-back" type="button" onClick={() => select()}>
      <ArrowLeft size={16} aria-hidden="true" />{t($ => $.agents.back)}
    </button>}
    <div className="page-heading">
      <div><h1 ref={heading} tabIndex={-1}>{selectedID ?? t($ => $.agents.title)}</h1>
        {selectedID === undefined && <p>{t($ => $.agents.description)}</p>}
      </div>
      <div className="page-heading-actions">
        {onOpenSessions && <Button variant="secondary" onClick={() => onOpenSessions(selectedID)}>
          {t($ => $.shell.nav.sessions)}
        </Button>}
        {onManageSecrets && <Button variant="secondary" onClick={onManageSecrets}>
          <KeyRound size={15} aria-hidden="true" />{t($ => $.agents.manageSecrets)}
        </Button>}
        {credentials && <Button variant="secondary" onClick={query.refresh} disabled={query.loading || query.refreshing}>
          <RefreshCw size={15} aria-hidden="true" />{query.refreshing ? t($ => $.agents.refreshing) : t($ => $.agents.refresh)}
        </Button>}
      </div>
    </div>
    {error ? <div className="notice" role="alert">
      <p>{t($ => $.agents.errors[error])}</p>
      {error === 'authentication' ? <Button onClick={onSignIn}>{t($ => $.agents.signIn)}</Button>
        : error !== 'not_found' && <Button variant="secondary" onClick={query.refresh}>{t($ => $.agents.retry)}</Button>}
    </div> : query.loading ? <p className="catalog-loading" role="status">
      <LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />
      {selectedID === undefined ? t($ => $.agents.loading) : t($ => $.agents.loadingDetail)}
    </p> : query.item ? <AgentDetails agent={query.item} /> : <>
      {items.length > 0 && <div className="catalog-toolbar">
        <label className="catalog-search"><Search size={17} aria-hidden="true" />
          <input type="search" aria-label={t($ => $.agents.search)} placeholder={t($ => $.agents.searchPlaceholder)} value={search} onChange={event => setSearch(event.target.value)} />
        </label>
        <p role="status">{filter ? t($ => $.agents.filtered, { count: visible.length, total: items.length }) : t($ => $.agents.count, { count: items.length })}</p>
      </div>}
      {visible.length > 0 ? <ul className="catalog-list">{visible.map(item => <li key={item.id}>
        <button type="button" className="catalog-row" aria-label={t($ => $.agents.open, { id: item.id })} onClick={() => select(item.id)}>
          <span className="catalog-row-main"><span className="catalog-row-name">{item.id}</span>
            <span className="catalog-row-description">{item.type}{item.server.hostname && <> · {item.server.hostname}</>}</span>
            <span className="catalog-row-meta"><AgentStatus status={item.status} />
              {item.stale && <span className="catalog-status" data-tone="warning">{t($ => $.agents.stale)}</span>}
              <span>{t($ => $.agents.actionReadiness, { ready: item.actions?.filter(action => action.status === 'ready').length ?? 0, total: item.actions?.length ?? 0 })}</span>
            </span>
            <span className="catalog-row-description">{item.work
              ? item.stale ? t($ => $.agents.lastWorkAction, { name: item.work.action_name }) : t($ => $.agents.workAction, { name: item.work.action_name })
              : t($ => $.agents.noWork)}</span>
          </span><ArrowUpRight size={18} aria-hidden="true" />
        </button>
      </li>)}</ul> : <div className="catalog-empty">
        <Bot size={26} strokeWidth={1.3} aria-hidden="true" /><h2>{items.length ? t($ => $.agents.noMatches) : t($ => $.agents.emptyTitle)}</h2>
        {items.length ? <Button variant="secondary" onClick={() => setSearch('')}>{t($ => $.agents.clearSearch)}</Button> : <p>{t($ => $.agents.emptyDescription)}</p>}
      </div>}
    </>}
    {credentials && !query.loading && !error && <p className="catalog-updated">{t($ => $.agents.polling)} {query.checkedAt !== undefined && t($ => $.agents.checked, { time: new Date(query.checkedAt).toLocaleTimeString() })}</p>}
  </section>;
}

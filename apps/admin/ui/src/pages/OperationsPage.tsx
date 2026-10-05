import { useEffect, useRef, useState } from 'react';
import { ArrowLeft, ArrowUpRight, LoaderCircle, RefreshCw, Search, Workflow } from 'lucide-react';
import { getOperation, listOperations } from '../api/operations';
import type { Operation } from '../api/operation-types';
import { OperationDetails, OperationStatus, operationProgress } from '../components/OperationDetails';
import { Button } from '../components/Button';
import type { Connection, Credentials } from '../connections/types';
import { useCatalogQuery } from '../hooks/useCatalogQuery';
import { useTranslation } from '../i18n';
import '../styles/catalog.css';
import '../styles/operations.css';

const knownStatuses = ['planning', 'running', 'waiting', 'completed', 'failed', 'cancelled'] as const;

export function OperationsPage({ connection, credentials, onSignIn }: {
  connection: Connection;
  credentials?: Credentials;
  onSignIn: () => void;
}) {
  const { t } = useTranslation();
  const [selectedID, setSelectedID] = useState<string>();
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState('');
  const heading = useRef<HTMLHeadingElement>(null);
  const focusHeading = useRef(false);
  const query = useCatalogQuery<Operation>({ url: connection.url, credentials, id: selectedID, list: listOperations, get: getOperation, pollInterval: 10_000 });
  const items = query.items ?? [];
  const filter = search.trim().toLocaleLowerCase();
  const statuses = [...new Set([...knownStatuses, ...items.map(item => item.status), ...(status ? [status] : [])])];
  const visible = items.filter(item => (!status || item.status === status) &&
    `${item.id} ${item.mission.name}`.toLocaleLowerCase().includes(filter));
  const error = !credentials ? 'authentication' : query.error?.kind;

  useEffect(() => {
    if (focusHeading.current) { heading.current?.focus(); focusHeading.current = false; }
  }, [selectedID]);

  function select(id?: string) { focusHeading.current = true; setSelectedID(id); }
  function clearFilters() { setSearch(''); setStatus(''); }

  return <section className="catalog-page operations-page">
    {selectedID !== undefined && <button className="catalog-back" type="button" onClick={() => select()}>
      <ArrowLeft size={16} aria-hidden="true" />{t($ => $.operations.back)}
    </button>}
    <div className="page-heading">
      <div><h1 ref={heading} tabIndex={-1}>{selectedID ?? t($ => $.operations.title)}</h1>
        {selectedID === undefined && <p>{t($ => $.operations.description)}</p>}
      </div>
      {credentials && <Button variant="secondary" onClick={query.refresh} disabled={query.refreshing}>
        <RefreshCw size={15} aria-hidden="true" />{query.refreshing ? t($ => $.operations.refreshing) : t($ => $.operations.refresh)}
      </Button>}
    </div>
    {error ? <div className="notice" role="alert">
      <p>{t($ => $.operations.errors[error])}</p>
      {error === 'authentication' ? <Button onClick={onSignIn}>{t($ => $.operations.signIn)}</Button>
        : error !== 'not_found' && <Button variant="secondary" onClick={query.refresh}>{t($ => $.operations.retry)}</Button>}
    </div> : query.loading ? <p className="catalog-loading" role="status">
      <LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />
      {selectedID === undefined ? t($ => $.operations.loading) : t($ => $.operations.loadingDetail)}
    </p> : query.item ? <OperationDetails operation={query.item} /> : <>
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
              <span>{t($ => $.operations.progress, operationProgress(item.tasks))}</span>
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
    {credentials && !query.loading && !error && <p className="catalog-updated">{t($ => $.operations.polling)} {query.checkedAt !== undefined && t($ => $.operations.checked, { time: new Date(query.checkedAt).toLocaleTimeString() })}</p>}
  </section>;
}

import { useEffect, useRef, useState } from 'react';
import { ArrowLeft, ArrowUpRight, LoaderCircle, RefreshCw, Search, Server } from 'lucide-react';
import { getController, listControllers } from '../api/controllers';
import type { Controller } from '../api/types';
import { ControllerDetails, ControllerStatus, ControllerTime } from '../components/ControllerDetails';
import { Button } from '../components/Button';
import type { Connection, Session } from '../connections/types';
import { useCatalogQuery } from '../hooks/useCatalogQuery';
import { useTranslation } from '../i18n';
import '../styles/catalog.css';

export function ControllersPage({ connection, credentials, onSignIn, selectedID: routeID, onSelect }: {
  connection: Connection;
  credentials?: Session;
  onSignIn: () => void;
  selectedID?: string;
  onSelect?: (id?: string) => void;
}) {
  const { t } = useTranslation();
  const [localID, setLocalID] = useState<string>();
  const selectedID = onSelect ? routeID : localID;
  const [search, setSearch] = useState('');
  const heading = useRef<HTMLHeadingElement>(null);
  const focusHeading = useRef(false);
  const query = useCatalogQuery<Controller>({ url: connection.url, credentials, id: selectedID, list: listControllers, get: getController, pollInterval: 10_000 });
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
      <ArrowLeft size={16} aria-hidden="true" />{t($ => $.controllers.back)}
    </button>}
    <div className="page-heading">
      <div><h1 ref={heading} tabIndex={-1}>{selectedID ?? t($ => $.controllers.title)}</h1>
        {selectedID === undefined && <p>{t($ => $.controllers.description)}</p>}
      </div>
      {credentials && <Button variant="secondary" onClick={query.refresh} disabled={query.loading || query.refreshing}>
        <RefreshCw size={15} aria-hidden="true" />{query.refreshing ? t($ => $.controllers.refreshing) : t($ => $.controllers.refresh)}
      </Button>}
    </div>
    {error ? <div className="notice" role="alert">
      <p>{t($ => $.controllers.errors[error])}</p>
      {error === 'authentication' ? <Button onClick={onSignIn}>{t($ => $.controllers.signIn)}</Button>
        : error !== 'not_found' && <Button variant="secondary" onClick={query.refresh}>{t($ => $.controllers.retry)}</Button>}
    </div> : query.loading ? <p className="catalog-loading" role="status">
      <LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />
      {selectedID === undefined ? t($ => $.controllers.loading) : t($ => $.controllers.loadingDetail)}
    </p> : query.item ? <ControllerDetails controller={query.item} /> : <>
      {items.length > 0 && <div className="catalog-toolbar">
        <label className="catalog-search"><Search size={17} aria-hidden="true" />
          <input type="search" aria-label={t($ => $.controllers.search)} placeholder={t($ => $.controllers.searchPlaceholder)} value={search} onChange={event => setSearch(event.target.value)} />
        </label>
        <p role="status">{filter ? t($ => $.controllers.filtered, { count: visible.length, total: items.length }) : t($ => $.controllers.count, { count: items.length })}</p>
      </div>}
      {visible.length > 0 ? <ul className="catalog-list">{visible.map(item => <li key={item.id}>
        <button type="button" className="catalog-row" aria-label={t($ => $.controllers.open, { id: item.id })} onClick={() => select(item.id)}>
          <span className="catalog-row-main"><span className="catalog-row-name">{item.id}</span>
            <span className="catalog-row-description">{item.type}{item.server.hostname && <> · {item.server.hostname}</>}</span>
            <span className="catalog-row-meta"><ControllerStatus status={item.status} />
              {item.stale && <span className="catalog-status" data-tone="warning">{t($ => $.controllers.stale)}</span>}
            </span>
            <span className="catalog-row-description">{t($ => $.controllers.lastHeartbeat)}: <ControllerTime value={item.last_heartbeat} /></span>
          </span><ArrowUpRight size={18} aria-hidden="true" />
        </button>
      </li>)}</ul> : <div className="catalog-empty">
        <Server size={26} strokeWidth={1.3} aria-hidden="true" /><h2>{items.length ? t($ => $.controllers.noMatches) : t($ => $.controllers.emptyTitle)}</h2>
        {items.length ? <Button variant="secondary" onClick={() => setSearch('')}>{t($ => $.controllers.clearSearch)}</Button> : <p>{t($ => $.controllers.emptyDescription)}</p>}
      </div>}
    </>}
    {credentials && !query.loading && !error && <p className="catalog-updated">{t($ => $.controllers.polling)} {query.checkedAt !== undefined && t($ => $.controllers.checked, { time: new Date(query.checkedAt).toLocaleTimeString() })}</p>}
  </section>;
}

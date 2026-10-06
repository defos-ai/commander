import { useEffect, useRef, useState } from 'react';
import { ArrowLeft, ArrowUpRight, LoaderCircle, RefreshCw, Search, Zap } from 'lucide-react';
import { getAction, listActions } from '../api/actions';
import type { Action } from '../api/types';
import { ActionDetails } from '../components/ActionDetails';
import { Button } from '../components/Button';
import type { Connection, Credentials } from '../connections/types';
import { useCatalogQuery } from '../hooks/useCatalogQuery';
import { useTranslation } from '../i18n';
import '../styles/catalog.css';

export function ActionsPage({ connection, credentials, onSignIn, selectedID: routeID, onSelect }: {
  connection: Connection;
  credentials?: Credentials;
  onSignIn: () => void;
  selectedID?: string;
  onSelect?: (id?: string) => void;
}) {
  const { t } = useTranslation();
  const [localID, setLocalID] = useState<string>();
  const name = onSelect ? routeID : localID;
  const [search, setSearch] = useState('');
  const heading = useRef<HTMLHeadingElement>(null);
  const focusHeading = useRef(false);
  const query = useCatalogQuery<Action>({ url: connection.url, credentials, id: name, list: listActions, get: getAction, pollInterval: 30_000 });
  const items = query.items ?? [];
  const filter = search.trim().toLocaleLowerCase();
  const visible = items.filter(item => `${item.name} ${item.description}`.toLocaleLowerCase().includes(filter));
  const error = !credentials ? 'authentication' : query.error?.kind;

  useEffect(() => {
    if (focusHeading.current) { heading.current?.focus(); focusHeading.current = false; }
  }, [name]);

  function select(value?: string) { focusHeading.current = true; if (onSelect) onSelect(value); else setLocalID(value); }

  return <section className="catalog-page">
    {name !== undefined && <button className="catalog-back" type="button" onClick={() => select()}>
      <ArrowLeft size={16} aria-hidden="true" />{t($ => $.actions.back)}
    </button>}
    <div className="page-heading">
      <div><h1 ref={heading} tabIndex={-1}>{name ?? t($ => $.actions.title)}</h1>
        {name === undefined && <p>{t($ => $.actions.description)}</p>}
      </div>
      {credentials && <Button variant="secondary" onClick={query.refresh} disabled={query.loading || query.refreshing}>
        <RefreshCw size={15} aria-hidden="true" />{query.refreshing ? t($ => $.actions.refreshing) : t($ => $.actions.refresh)}
      </Button>}
    </div>
    {error ? <div className="notice" role="alert">
      <p>{t($ => $.actions.errors[error])}</p>
      {error === 'authentication' ? <Button onClick={onSignIn}>{t($ => $.actions.signIn)}</Button>
        : error !== 'not_found' && <Button variant="secondary" onClick={query.refresh}>{t($ => $.actions.retry)}</Button>}
    </div> : query.loading ? <p className="catalog-loading" role="status">
      <LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />
      {name === undefined ? t($ => $.actions.loading) : t($ => $.actions.loadingDetail)}
    </p> : query.item ? <ActionDetails action={query.item} /> : <>
      {items.length > 0 && <div className="catalog-toolbar">
        <label className="catalog-search"><Search size={17} aria-hidden="true" />
          <input type="search" aria-label={t($ => $.actions.search)} placeholder={t($ => $.actions.searchPlaceholder)}
            value={search} onChange={event => setSearch(event.target.value)} />
        </label>
        <p role="status">{filter ? t($ => $.actions.filtered, { count: visible.length, total: items.length }) : t($ => $.actions.count, { count: items.length })}</p>
      </div>}
      {visible.length > 0 ? <ul className="catalog-list">
        {visible.map(item => <li key={item.name}><button type="button" className="catalog-row"
          aria-label={t($ => $.actions.open, { name: item.name })} onClick={() => select(item.name)}>
          <span className="catalog-row-main"><span className="catalog-row-name">{item.name}</span><span className="catalog-row-description">{item.description}</span>
            <span className="catalog-row-meta">{t($ => $.actions.permissionsCount, { count: item.required_permissions?.length ?? 0 })}</span>
          </span><ArrowUpRight size={18} aria-hidden="true" />
        </button></li>)}
      </ul> : <div className="catalog-empty">
        <Zap size={26} strokeWidth={1.3} aria-hidden="true" />
        <h2>{items.length ? t($ => $.actions.noMatches) : t($ => $.actions.emptyTitle)}</h2>
        {items.length ? <Button variant="secondary" onClick={() => setSearch('')}>{t($ => $.actions.clearSearch)}</Button> : <p>{t($ => $.actions.emptyDescription)}</p>}
      </div>}
    </>}
    {!error && !query.loading && query.checkedAt && <p className="catalog-updated">{t($ => $.actions.updated, { time: new Date(query.checkedAt).toLocaleTimeString() })}</p>}
  </section>;
}

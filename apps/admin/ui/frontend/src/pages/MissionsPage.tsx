import { useEffect, useRef, useState } from 'react';
import { ArrowLeft, ArrowUpRight, Compass, LoaderCircle, RefreshCw, Search } from 'lucide-react';
import { Button } from '../components/Button';
import { MissionDetails } from '../components/MissionDetails';
import type { Connection, Credentials } from '../connections/types';
import { useMissionQuery } from '../hooks/useMissionQuery';
import { useTranslation } from '../i18n';
import '../styles/missions.css';

export function MissionsPage({ connection, credentials, onSignIn, selectedID: routeID, onSelect }: {
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
  const query = useMissionQuery(connection.url, credentials, name);
  const items = query.content && 'items' in query.content ? query.content.items : [];
  const mission = query.content && 'mission' in query.content ? query.content.mission : undefined;
  const filter = search.trim().toLocaleLowerCase();
  const visible = items.filter(item => `${item.name} ${item.objective}`.toLocaleLowerCase().includes(filter));
  const error = !credentials ? 'authentication' : query.error?.kind;

  useEffect(() => {
    if (focusHeading.current) { heading.current?.focus(); focusHeading.current = false; }
  }, [name]);

  function select(value?: string) { focusHeading.current = true; if (onSelect) onSelect(value); else setLocalID(value); }

  return <section className="missions-page">
    {name !== undefined && <button className="missions-back" type="button" onClick={() => select()}>
      <ArrowLeft size={16} aria-hidden="true" />{t($ => $.missions.back)}
    </button>}
    <div className="page-heading">
      <div><h1 ref={heading} tabIndex={-1}>{name ?? t($ => $.missions.title)}</h1>
        {name === undefined && <p>{t($ => $.missions.description)}</p>}
      </div>
      {credentials && <Button variant="secondary" onClick={query.refresh} disabled={query.loading}>
        <RefreshCw size={15} aria-hidden="true" />{t($ => $.missions.refresh)}
      </Button>}
    </div>
    {error ? <div className="notice" role="alert">
      <p>{t($ => $.missions.errors[error])}</p>
      {error === 'authentication' ? <Button onClick={onSignIn}>{t($ => $.missions.signIn)}</Button>
        : error !== 'not_found' && <Button variant="secondary" onClick={query.refresh}>{t($ => $.missions.retry)}</Button>}
    </div> : query.loading ? <p className="missions-loading" role="status">
      <LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />
      {name === undefined ? t($ => $.missions.loading) : t($ => $.missions.loadingDetail)}
    </p> : mission ? <MissionDetails mission={mission} /> : <>
      {items.length > 0 && <div className="missions-toolbar">
        <label className="mission-search"><Search size={17} aria-hidden="true" />
          <input type="search" aria-label={t($ => $.missions.search)} placeholder={t($ => $.missions.searchPlaceholder)}
            value={search} onChange={event => setSearch(event.target.value)} />
        </label>
        <p role="status">{filter ? t($ => $.missions.filtered, { count: visible.length, total: items.length }) : t($ => $.missions.count, { count: items.length })}</p>
      </div>}
      {visible.length > 0 ? <ul className="mission-list">
        {visible.map(item => <li key={item.name}><button type="button" className="mission-row"
          aria-label={t($ => $.missions.open, { name: item.name })} onClick={() => select(item.name)}>
          <span className="mission-row-main"><span className="mission-row-name">{item.name}</span><span className="mission-row-objective">{item.objective}</span>
            <span className="mission-row-meta"><span>{t($ => $.missions.actions, { count: item.required_actions?.length ?? 0 })}</span><span>{t($ => $.missions.automationCount, { count: Object.keys(item.automations ?? {}).length })}</span></span>
          </span><ArrowUpRight size={18} aria-hidden="true" />
        </button></li>)}
      </ul> : <div className="missions-empty">
        <Compass size={26} strokeWidth={1.3} aria-hidden="true" />
        <h2>{items.length ? t($ => $.missions.noMatches) : t($ => $.missions.emptyTitle)}</h2>
        {items.length ? <Button variant="secondary" onClick={() => setSearch('')}>{t($ => $.missions.clearSearch)}</Button> : <p>{t($ => $.missions.emptyDescription)}</p>}
      </div>}
    </>}
  </section>;
}

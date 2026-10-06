import { RefreshCw } from 'lucide-react';
import { Button } from '../components/Button';
import type { ConnectionCheck } from '../connections/types';
import { useTranslation } from '../i18n';

export function OverviewPage({ check, refreshing, onRefresh, onSignIn }: {
  check?: ConnectionCheck;
  refreshing: boolean;
  onRefresh: () => void;
  onSignIn: () => void;
}) {
  const { t } = useTranslation();
  const status = check?.status ?? 'checking';
  return <section>
    <div className="page-heading">
      <div><h1>{t($ => $.overview.title)}</h1><p>{t($ => $.overview.description)}</p></div>
      <Button variant="secondary" onClick={onRefresh} disabled={refreshing}>
        <RefreshCw size={15} aria-hidden="true" />{refreshing ? t($ => $.overview.refreshing) : t($ => $.overview.refresh)}
      </Button>
    </div>
    <div className="health-grid" aria-live="polite">
      <div className="health-card"><h2>{t($ => $.overview.status)}</h2><p className="connection-card-status" data-status={status}>
        <span className="status-dot" aria-hidden="true" />{t($ => $.connections.status[status])}
      </p></div>
      {(['api', 'database'] as const).map(service => <div className="health-card" key={service}>
        <h2>{t($ => $.overview[service])}</h2><p data-health={check?.[service] ?? 'unknown'}>
          <span className="status-dot" aria-hidden="true" />{t($ => $.overview.health[check?.[service] ?? 'unknown'])}
        </p>
      </div>)}
    </div>
    {status === 'authentication' && <div className="notice"><p>{t($ => $.overview.authentication)}</p><Button onClick={onSignIn}>{t($ => $.overview.signIn)}</Button></div>}
    {(status === 'unreachable' || status === 'degraded') && <p className="notice" role="status">{t($ => $.overview[status])}</p>}
    <div className="health-caption">
      {check?.checkedAt && <p>{t($ => $.overview.updated, { time: new Date(check.checkedAt).toLocaleTimeString() })}</p>}
      <p>{t($ => $.overview.polling)}</p>
      <p>{t($ => $.overview.scope)}</p>
    </div>
  </section>;
}

import { RefreshCw } from 'lucide-react';
import { Button } from '../components/Button';
import type { ConnectionCheck } from '../connections/types';
import { useTranslation } from '../i18n';

export function StatusPage({ check, refreshing, onRefresh, onSignIn }: {
  check?: ConnectionCheck;
  refreshing: boolean;
  onRefresh: () => void;
  onSignIn: () => void;
}) {
  const { t } = useTranslation();
  const status = check?.status ?? 'checking';
  return <section>
    <div className="page-heading">
      <div><h1>{t($ => $.status.title)}</h1><p>{t($ => $.status.description)}</p></div>
      <Button variant="secondary" onClick={onRefresh} disabled={refreshing}>
        <RefreshCw size={15} aria-hidden="true" />{refreshing ? t($ => $.status.refreshing) : t($ => $.status.refresh)}
      </Button>
    </div>
    <div className="health-grid" aria-live="polite">
      <div className="health-card"><h2>{t($ => $.status.status)}</h2><p className="connection-card-status" data-status={status}>
        <span className="status-dot" aria-hidden="true" />{t($ => $.connections.status[status])}
      </p></div>
      {(['api', 'database'] as const).map(service => <div className="health-card" key={service}>
        <h2>{t($ => $.status[service])}</h2><p data-health={check?.[service] ?? 'unknown'}>
          <span className="status-dot" aria-hidden="true" />{t($ => $.status.health[check?.[service] ?? 'unknown'])}
        </p>
      </div>)}
    </div>
    {status === 'authentication' && <div className="notice"><p>{t($ => $.status.authentication)}</p><Button onClick={onSignIn}>{t($ => $.status.signIn)}</Button></div>}
    {(status === 'unreachable' || status === 'degraded') && <p className="notice" role="status">{t($ => $.status[status])}</p>}
    <div className="health-caption">
      {check?.checkedAt && <p>{t($ => $.status.updated, { time: new Date(check.checkedAt).toLocaleTimeString() })}</p>}
      <p>{t($ => $.status.polling)}</p>
      <p>{t($ => $.status.scope)}</p>
    </div>
  </section>;
}

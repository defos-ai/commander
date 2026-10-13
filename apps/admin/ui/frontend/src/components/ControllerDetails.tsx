import type { Controller } from '../api/types';
import { useTranslation } from '../i18n';

export function ControllerStatus({ status }: { status: string }) {
  const { t } = useTranslation();
  const label = status === 'starting' || status === 'ready' || status === 'degraded' || status === 'draining' || status === 'stopped' || status === 'unavailable'
    ? t($ => $.controllers.status[status]) : status;
  const tone = status === 'ready' ? 'ready' : status === 'degraded' || status === 'unavailable' ? 'warning' : 'muted';
  return <span className="catalog-status" data-tone={tone}>{label}</span>;
}

export function ControllerTime({ value }: { value: string }) {
  const { t } = useTranslation();
  const date = new Date(value);
  return !Number.isNaN(date.getTime()) && !value.startsWith('0001-')
    ? <time dateTime={value} title={value}>{date.toLocaleString()}</time>
    : <span className="catalog-muted">{t($ => $.controllers.notReported)}</span>;
}

export function ControllerDetails({ controller }: { controller: Controller }) {
  const { t } = useTranslation();
  return <div className="catalog-details">
    {controller.stale && <p className="notice" role="status">{t($ => $.controllers.staleDescription)}</p>}
    <section className="catalog-section"><h2>{t($ => $.controllers.snapshot)}</h2>
      <dl className="detail-list">
        <div><dt>{t($ => $.controllers.type)}</dt><dd>{controller.type || t($ => $.controllers.notReported)}</dd></div>
        <div><dt>{t($ => $.controllers.statusLabel)}</dt><dd><ControllerStatus status={controller.status} />{controller.stale && <> · <span className="catalog-status" data-tone="warning">{t($ => $.controllers.stale)}</span></>}</dd></div>
        {controller.status_reason && <div><dt>{t($ => $.controllers.statusReason)}</dt><dd>{controller.status_reason}</dd></div>}
        <div><dt>{t($ => $.controllers.hostname)}</dt><dd>{controller.server.hostname || t($ => $.controllers.notReported)}</dd></div>
        <div><dt>{t($ => $.controllers.operatingSystem)}</dt><dd>{controller.server.os || t($ => $.controllers.notReported)}</dd></div>
        <div><dt>{t($ => $.controllers.architecture)}</dt><dd>{controller.server.arch || t($ => $.controllers.notReported)}</dd></div>
        <div><dt>{t($ => $.controllers.version)}</dt><dd>{controller.version || t($ => $.controllers.notReported)}</dd></div>
        <div><dt>{t($ => $.controllers.agentSelector)}</dt><dd>{controller.agent_selector || t($ => $.controllers.notReported)}</dd></div>
        <div><dt>{t($ => $.controllers.ruleSelector)}</dt><dd>{controller.rule_selector || t($ => $.controllers.notReported)}</dd></div>
        <div><dt>{t($ => $.controllers.startedAt)}</dt><dd><ControllerTime value={controller.started_at} /></dd></div>
        <div><dt>{t($ => $.controllers.lastHeartbeat)}</dt><dd><ControllerTime value={controller.last_heartbeat} /></dd></div>
        <div><dt>{t($ => $.controllers.leaseExpiresAt)}</dt><dd><ControllerTime value={controller.lease_expires_at} /></dd></div>
      </dl>
    </section>
  </div>;
}

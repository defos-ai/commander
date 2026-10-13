import type { Agent } from '../api/types';
import { useTranslation } from '../i18n';
import { ActionStatus } from './ActionDetails';

export function AgentStatus({ status }: { status: string }) {
  const { t } = useTranslation();
  const label = status === 'idle' || status === 'discovering' || status === 'ready' || status === 'working' || status === 'unavailable' || status === 'stopped'
    ? t($ => $.agents.status[status]) : status;
  const tone = status === 'ready' ? 'ready' : status === 'unavailable' ? 'warning' : 'muted';
  return <span className="catalog-status" data-tone={tone}>{label}</span>;
}

function ReportedTime({ value }: { value?: string }) {
  const { t } = useTranslation();
  const date = value ? new Date(value) : undefined;
  return date && !Number.isNaN(date.getTime()) && !value?.startsWith('0001-')
    ? <time dateTime={value} title={value}>{date.toLocaleString()}</time>
    : <span className="catalog-muted">{t($ => $.agents.notReported)}</span>;
}

function Labels({ values }: { values: string[] | null | undefined }) {
  const { t } = useTranslation();
  return values?.length ? <ul className="catalog-tags">{values.map((value, index) => <li key={`${index}:${value}`}>{value}</li>)}</ul>
    : <p className="catalog-muted">{t($ => $.agents.notReported)}</p>;
}

export function AgentDetails({ agent }: { agent: Agent }) {
  const { t } = useTranslation();
  const work = agent.work;
  return <div className="catalog-details">
    {agent.stale && <p className="notice" role="status">{t($ => $.agents.staleDescription)}</p>}
    <section className="catalog-section"><h2>{t($ => $.agents.snapshot)}</h2>
      <dl className="detail-list">
        <div><dt>{t($ => $.agents.type)}</dt><dd>{agent.type}</dd></div>
        <div><dt>{t($ => $.agents.statusLabel)}</dt><dd><AgentStatus status={agent.status} />{agent.stale && <> · <span className="catalog-status" data-tone="warning">{t($ => $.agents.stale)}</span></>}</dd></div>
        <div><dt>{t($ => $.agents.hostname)}</dt><dd>{agent.server.hostname || t($ => $.agents.notReported)}</dd></div>
        <div><dt>{t($ => $.agents.ipAddress)}</dt><dd>{agent.server.ip_address || t($ => $.agents.notReported)}</dd></div>
        <div><dt>{t($ => $.agents.lastSeen)}</dt><dd><ReportedTime value={agent.last_seen} /></dd></div>
        <div><dt>{t($ => $.agents.lastHeartbeat)}</dt><dd><ReportedTime value={agent.last_heartbeat} /></dd></div>
        {agent.observed_at && <div><dt>{t($ => $.agents.observedAt)}</dt><dd><ReportedTime value={agent.observed_at} /></dd></div>}
        {agent.lease_expires_at && <div><dt>{t($ => $.agents.leaseExpiresAt)}</dt><dd><ReportedTime value={agent.lease_expires_at} /></dd></div>}
      </dl>
    </section>
    <section className="catalog-section"><h2>{agent.stale ? t($ => $.agents.lastWork) : t($ => $.agents.work)}</h2>
      {work ? <dl className="detail-list">
        <div><dt>{t($ => $.agents.action)}</dt><dd><code>{work.action_name}</code></dd></div>
        {work.operation_id && <div><dt>{t($ => $.agents.operation)}</dt><dd><code>{work.operation_id}</code></dd></div>}
        {work.task_id && <div><dt>{t($ => $.agents.task)}</dt><dd><code>{work.task_id}</code></dd></div>}
        {work.step_id && <div><dt>{t($ => $.agents.step)}</dt><dd><code>{work.step_id}</code></dd></div>}
        {work.command_id && <div><dt>{t($ => $.agents.command)}</dt><dd><code>{work.command_id}</code></dd></div>}
      </dl> : <p className="catalog-muted">{t($ => $.agents.noWork)}</p>}
    </section>
    <section className="catalog-section"><h2>{t($ => $.agents.actions)}</h2><p className="catalog-muted">{t($ => $.agents.actionsDescription)}</p>
      {agent.actions?.length ? <ul className="agent-action-list">{agent.actions.map(action => <li key={action.name}>
        <details className="agent-action"><summary><span>{action.name}</span><ActionStatus status={action.status} /></summary>
          <p>{action.description}</p>
          {action.status_reason && <p>{action.status_reason}</p>}
          {Boolean(action.missing_requirements?.length) && <div><h3>{t($ => $.agents.missingRequirements)}</h3>
            <ul className="catalog-tags">{action.missing_requirements?.map((requirement, index) => {
              const kind = requirement.kind;
              return <li key={`${index}:${kind}:${requirement.name}`}>
              {kind === 'executable' || kind === 'configuration' ? t($ => $.agents.requirementKinds[kind]) : kind}: <code>{requirement.name}</code>
              {requirement.secret && <> · {t($ => $.agents.secret)}</>}
            </li>; })}</ul>
          </div>}
        </details>
      </li>)}</ul> : <p className="catalog-muted">{t($ => $.agents.noActions)}</p>}
    </section>
    <section className="catalog-section"><h2>{t($ => $.agents.responsibilities)}</h2><Labels values={agent.responsibilities} /></section>
  </div>;
}

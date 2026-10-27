import type { RuntimeSession } from '../api/runtime-sessions';
import { useTranslation } from '../i18n';

export function SessionState({ state }: { state: string }) {
  const { t } = useTranslation();
  const known = state === 'starting' || state === 'listening' || state === 'verifying' || state === 'connected' || state === 'unverified' || state === 'lost' || state === 'closed' || state === 'failed';
  return <span className="catalog-status" data-tone={state === 'connected' ? 'ready' : state === 'lost' || state === 'failed' ? 'warning' : 'muted'}>
    {known ? t($ => $.runtimeSessions.states[state]) : state}
  </span>;
}

export function SessionDisposition({ disposition }: { disposition?: string }) {
  const { t } = useTranslation();
  const known = disposition === 'reserved' || disposition === 'active' || disposition === 'closing' || disposition === 'released';
  return <span className="session-disposition">{known ? t($ => $.runtimeSessions.dispositions[disposition]) : disposition || t($ => $.runtimeSessions.notReported)}</span>;
}

function ReportedTime({ value }: { value?: string }) {
  const { t } = useTranslation();
  const date = value ? new Date(value) : undefined;
  return date && !Number.isNaN(date.getTime()) && !value?.startsWith('0001-')
    ? <time dateTime={value} title={value}>{date.toLocaleString()}</time>
    : <span className="catalog-muted">{t($ => $.runtimeSessions.notReported)}</span>;
}

export function SessionDetails({ session, onOpenAgent }: { session: RuntimeSession; onOpenAgent?: (id: string) => void }) {
  const { t } = useTranslation();
  const releaseReason = session.release_reason;
  const knownRelease = releaseReason === 'closed' || releaseReason === 'abandoned' || releaseReason === 'cancelled_before_effect';
  return <div className="catalog-details">
    {session.disposition === 'closing' && <p className="notice" role="status">{t($ => $.runtimeSessions.closePending)}</p>}
    {session.disposition === 'released' && <p className="notice" role="status">{t($ => $.runtimeSessions.retainedHistory)}</p>}
    <section className="catalog-section"><h2>{t($ => $.runtimeSessions.snapshot)}</h2>
      <dl className="detail-list">
        <div><dt>{t($ => $.runtimeSessions.id)}</dt><dd><code>{session.id}</code></dd></div>
        <div><dt>{t($ => $.runtimeSessions.label)}</dt><dd>{session.label}</dd></div>
        <div><dt>{t($ => $.runtimeSessions.kind)}</dt><dd>{session.kind}</dd></div>
        <div><dt>{t($ => $.runtimeSessions.scope)}</dt><dd><code>{session.scope_id}</code></dd></div>
        <div><dt>{t($ => $.runtimeSessions.owner)}</dt><dd>{session.owner_agent_id
          ? onOpenAgent ? <button className="session-owner-link" type="button" aria-label={t($ => $.runtimeSessions.openOwner, { id: session.owner_agent_id })} onClick={() => onOpenAgent(session.owner_agent_id)}>{session.owner_agent_id}</button>
            : <code>{session.owner_agent_id}</code>
          : t($ => $.runtimeSessions.notReported)}</dd></div>
        <div><dt>{t($ => $.runtimeSessions.state)}</dt><dd><SessionState state={session.state} /></dd></div>
        <div><dt>{t($ => $.runtimeSessions.disposition)}</dt><dd><SessionDisposition disposition={session.disposition} /></dd></div>
        <div><dt>{t($ => $.runtimeSessions.activity)}</dt><dd>{session.busy ? t($ => $.runtimeSessions.busy) : t($ => $.runtimeSessions.idle)}</dd></div>
        <div><dt>{t($ => $.runtimeSessions.revision)}</dt><dd>{session.revision}</dd></div>
        {session.reason && <div><dt>{t($ => $.runtimeSessions.reason)}</dt><dd>{session.reason}</dd></div>}
        {releaseReason && <div><dt>{t($ => $.runtimeSessions.releaseReason)}</dt><dd>{knownRelease ? t($ => $.runtimeSessions.releaseReasons[releaseReason]) : releaseReason}</dd></div>}
      </dl>
    </section>
    <section className="catalog-section"><h2>{t($ => $.runtimeSessions.connectionDetails)}</h2>
      <dl className="detail-list">{(['endpoint', 'peer', 'hostname', 'user'] as const).map(name => <div key={name}>
        <dt>{t($ => $.runtimeSessions[name])}</dt><dd>{session[name] || t($ => $.runtimeSessions.notReported)}</dd>
      </div>)}</dl>
    </section>
    <section className="catalog-section"><h2>{t($ => $.runtimeSessions.timestamps)}</h2>
      <dl className="detail-list">
        <div><dt>{t($ => $.runtimeSessions.createdAt)}</dt><dd><ReportedTime value={session.created_at} /></dd></div>
        <div><dt>{t($ => $.runtimeSessions.updatedAt)}</dt><dd><ReportedTime value={session.updated_at} /></dd></div>
        <div><dt>{t($ => $.runtimeSessions.connectedAt)}</dt><dd><ReportedTime value={session.connected_at} /></dd></div>
        <div><dt>{t($ => $.runtimeSessions.verifiedAt)}</dt><dd><ReportedTime value={session.verified_at} /></dd></div>
      </dl>
    </section>
  </div>;
}

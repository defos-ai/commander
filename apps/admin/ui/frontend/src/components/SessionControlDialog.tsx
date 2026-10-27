import { useEffect, useRef, useState, type FormEvent } from 'react';
import { LoaderCircle } from 'lucide-react';
import { ApiError } from '../api/client';
import { controlRuntimeSession, getRuntimeSession, SessionControlError, type RuntimeSession, type SessionControlMethod } from '../api/runtime-sessions';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';

export function canControlSession(session: RuntimeSession, method: SessionControlMethod) {
  if (!session.owner_agent_id || session.disposition === 'released') return false;
  if (method === 'inspect') return true;
  if (method === 'abandon') return session.state === 'lost' || session.state === 'failed';
  return session.disposition !== 'closing';
}

export function SessionControlDialog({ connection, credentials, session, method, available, onClose, onCompleted, onRefresh, onSubmitted, onReadBack }: {
  connection: Connection; credentials: Session; session: RuntimeSession; method: SessionControlMethod; available: boolean;
  onClose: () => void; onCompleted: (session: RuntimeSession, requestID: string) => void; onRefresh: () => void;
  onSubmitted: (requestID: string) => void; onReadBack: (session: RuntimeSession, requestID: string) => void;
}) {
  const { t } = useTranslation();
  const [requestID] = useState(() => crypto.randomUUID());
  const [confirmed, setConfirmed] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [reading, setReading] = useState(false);
  const [error, setError] = useState<SessionControlError>();
  const [readError, setReadError] = useState<ApiError>();
  const [saved, setSaved] = useState<RuntimeSession>();
  const pending = useRef<AbortController | null>(null);
  const sent = useRef(false);
  const requiresConfirmation = method === 'close' || method === 'abandon';
  const allowed = available && canControlSession(session, method);

  useEffect(() => () => { pending.current?.abort(); pending.current = null; }, [connection.url, credentials, session.id, session.scope_id, session.owner_agent_id, method]);

  function close() { pending.current?.abort(); pending.current = null; onClose(); }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (sent.current || pending.current || !allowed || requiresConfirmation && !confirmed) return;
    const controller = new AbortController();
    pending.current = controller;
    sent.current = true;
    onSubmitted(requestID);
    setSubmitting(true);
    try {
      const result = await controlRuntimeSession(connection.url, credentials, session, method, requestID, controller.signal);
      if (!controller.signal.aborted && pending.current === controller) onCompleted(result, requestID);
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) setError(cause instanceof SessionControlError ? cause : new SessionControlError('unknown', requestID));
    } finally {
      if (!controller.signal.aborted && pending.current === controller) { pending.current = null; setSubmitting(false); }
    }
  }

  async function inspectSaved() {
    if (pending.current) return;
    const controller = new AbortController();
    pending.current = controller;
    setReading(true);
    setReadError(undefined);
    try {
      const result = await getRuntimeSession(connection.url, credentials, session.id, controller.signal);
      if (result.scope_id !== session.scope_id || result.owner_agent_id !== session.owner_agent_id) throw new ApiError('invalid_response');
      if (!controller.signal.aborted && pending.current === controller) { setSaved(result); onReadBack(result, requestID); onRefresh(); }
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) setReadError(cause instanceof ApiError ? cause : new ApiError('unavailable'));
    } finally {
      if (!controller.signal.aborted && pending.current === controller) { pending.current = null; setReading(false); }
    }
  }

  return <Dialog open title={t($ => $.runtimeSessions.methods[method])} description={t($ => $.runtimeSessions.descriptions[method])} onClose={close}>
    <form className="session-control-form" onSubmit={submit}>
      <p className="form-hint">{t($ => $.runtimeSessions.id)}: <code>{session.id}</code></p>
      <p className="form-hint">{t($ => $.runtimeSessions.owner)}: <code>{session.owner_agent_id}</code></p>
      {requiresConfirmation && !error && <label className="session-confirmation"><input type="checkbox" checked={confirmed} disabled={submitting || !allowed} onChange={event => setConfirmed(event.target.checked)} />
        {method === 'close' ? t($ => $.runtimeSessions.confirmClose) : t($ => $.runtimeSessions.confirmAbandon)}
      </label>}
      {!allowed && !error && <p className="notice" role="status">{t($ => $.runtimeSessions.unavailableControl)}</p>}
      {error && <div className="notice" role="alert"><p>{t($ => $.runtimeSessions.controlErrors[error.kind])}</p>
        <p className="session-request-id">{t($ => $.runtimeSessions.requestID)}: <code>{requestID}</code></p>
        <Button variant="secondary" disabled={reading} onClick={() => void inspectSaved()}>{reading ? t($ => $.runtimeSessions.readingSaved) : t($ => $.runtimeSessions.inspectSaved)}</Button>
      </div>}
      {readError && <p className="notice" role="alert">{t($ => $.runtimeSessions.errors[readError.kind])}</p>}
      {saved && <div className="notice" role="status"><p>{t($ => $.runtimeSessions.savedState, { state: saved.state, disposition: saved.disposition || t($ => $.runtimeSessions.notReported) })}</p>
        <p>{t($ => $.runtimeSessions.readbackNote)}</p>
        {saved.disposition === 'closing' && <p>{t($ => $.runtimeSessions.closePending)}</p>}
      </div>}
      {submitting && <p className="form-hint" role="status">{t($ => $.runtimeSessions.submittedNote)}</p>}
      <div className="dialog-actions"><Button variant="secondary" onClick={close}>{error ? t($ => $.runtimeSessions.dismiss) : t($ => $.runtimeSessions.cancel)}</Button>
        {!error && <Button type="submit" className={requiresConfirmation ? 'danger-button' : undefined} disabled={submitting || !allowed || requiresConfirmation && !confirmed} aria-busy={submitting}>
          {submitting && <LoaderCircle className="connection-form-spinner" size={16} aria-hidden="true" />}
          {submitting ? t($ => $.runtimeSessions.submitting) : t($ => $.runtimeSessions.methods[method])}
        </Button>}
      </div>
    </form>
  </Dialog>;
}

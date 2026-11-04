import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { LoaderCircle } from 'lucide-react';
import { getTransientSecretRequest, provideTransientSecret, SecretError, type TransientSecretRequest } from '../api/secrets';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';

type Props = {
  connection: Connection;
  credentials: Session;
  request: TransientSecretRequest;
  submitted: boolean;
  onSubmitted: () => void;
  onRejected: () => void;
  onClose: () => void;
  onRefresh: () => void;
  onOpenOperation?: (id: string) => void;
};

function matchesRequest(saved: TransientSecretRequest, request: TransientSecretRequest) {
  return saved.id === request.id && saved.name === request.name && saved.agent_id === request.agent_id &&
    saved.agent_type === request.agent_type && saved.operation_id === request.operation_id &&
    saved.task_id === request.task_id && saved.command_id === request.command_id &&
    saved.requested_at === request.requested_at && saved.expires_at === request.expires_at;
}

export function TransientSecretDialog(props: Props) {
  return <TransientSecretContent key={`${props.connection.id}\0${props.connection.url}\0${props.request.id}`} {...props} />;
}

function TransientSecretContent({ connection, credentials, request, submitted, onSubmitted, onRejected,
  onClose, onRefresh, onOpenOperation }: Props) {
  const { t } = useTranslation();
  const identity = useId();
  const [saved, setSaved] = useState(request);
  const [verified, setVerified] = useState(false);
  const [value, setValue] = useState('');
  const [now, setNow] = useState(Date.now());
  const [attempted, setAttempted] = useState(submitted);
  const [accepted, setAccepted] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [reading, setReading] = useState(false);
  const [error, setError] = useState<SecretError>();
  const [readError, setReadError] = useState<SecretError>();
  const read = useRef<AbortController | null>(null);
  const write = useRef<AbortController | null>(null);
  const sent = useRef(submitted);
  const input = useRef<HTMLInputElement>(null);
  const refreshedTerminal = useRef(false);
  const refresh = useRef(onRefresh);
  refresh.current = onRefresh;
  const expired = now >= Date.parse(request.expires_at);
  const status = saved.status === 'pending' && expired ? 'expired' : saved.status;
  const pending = status === 'pending';
  const locked = submitted || attempted;
  const canEnter = pending && verified && !locked && !submitting && !readError;

  const inspect = useCallback(async () => {
    if (read.current) return;
    const controller = new AbortController();
    read.current = controller;
    setReading(true);
    setReadError(undefined);
    try {
      const current = await getTransientSecretRequest(connection.url, credentials, request.id, controller.signal);
      if (controller.signal.aborted || read.current !== controller) return;
      if (!matchesRequest(current, request)) throw new SecretError('invalid_response');
      setSaved(current);
      setVerified(true);
      if (current.status !== 'pending') {
        setValue('');
        if (!refreshedTerminal.current) { refreshedTerminal.current = true; refresh.current(); }
      }
    } catch (cause) {
      if (!controller.signal.aborted && read.current === controller) {
        setVerified(false);
        setReadError(cause instanceof SecretError ? cause : new SecretError('unavailable'));
      }
    } finally {
      if (read.current === controller) { read.current = null; setReading(false); }
    }
  }, [connection.url, credentials, request.id, request.name, request.agent_id, request.agent_type,
    request.operation_id, request.task_id, request.command_id, request.requested_at, request.expires_at]);

  useEffect(() => {
    void inspect();
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => {
      clearInterval(timer);
      read.current?.abort(); read.current = null;
      write.current?.abort(); write.current = null;
    };
  }, [inspect]);

  useEffect(() => {
    if (!pending) { setValue(''); return; }
    const timer = setInterval(() => { void inspect(); }, 2000);
    return () => clearInterval(timer);
  }, [pending, inspect]);

  useEffect(() => { if (canEnter) input.current?.focus(); }, [canEnter]);

  function close() {
    setValue('');
    read.current?.abort(); read.current = null;
    write.current?.abort(); write.current = null;
    if (sent.current) refresh.current();
    onClose();
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (sent.current || write.current || !canEnter || !value || Date.now() >= Date.parse(request.expires_at)) return;
    const controller = new AbortController();
    write.current = controller;
    sent.current = true;
    setAttempted(true);
    onSubmitted();
    setSubmitting(true);
    setError(undefined);
    const provided = value;
    setValue('');
    try {
      await provideTransientSecret(connection.url, credentials, request.id, provided, controller.signal);
      if (!controller.signal.aborted && write.current === controller) setAccepted(true);
    } catch (cause) {
      if (!controller.signal.aborted && write.current === controller) {
        const failure = cause instanceof SecretError ? cause : new SecretError('unknown');
        setError(failure);
        if (failure.kind === 'invalid_input') {
          sent.current = false;
          setAttempted(false);
          setVerified(false);
          onRejected();
        }
      }
    } finally {
      if (write.current === controller) {
        write.current = null;
        setSubmitting(false);
        void inspect();
        refresh.current();
      }
    }
  }

  return <Dialog open title={t($ => $.secrets.oneTime.dialogTitle)} description={t($ => $.secrets.oneTime.description)} onClose={close}>
    <form className="secret-form transient-secret-form" onSubmit={submit} autoComplete="off">
      <p className="secret-key"><strong>{request.name}</strong></p>
      <dl className="transient-secret-details">
        <div><dt>{t($ => $.secrets.oneTime.agent)}</dt><dd><code>{request.agent_id}</code></dd></div>
        <div><dt>{t($ => $.secrets.oneTime.operation)}</dt><dd><code>{request.operation_id}</code></dd></div>
        <div><dt>{t($ => $.secrets.oneTime.task)}</dt><dd><code>{request.task_id}</code></dd></div>
        <div><dt>{t($ => $.secrets.oneTime.expiry)}</dt><dd>{new Date(request.expires_at).toLocaleString()}</dd></div>
      </dl>
      <p className="notice" role="status">{t($ => $.secrets.oneTime.status[status])}
        {status === 'fulfilled' && <> {t($ => $.secrets.oneTime.receivedHint)}</>}
        {status === 'expired' && <> {t($ => $.secrets.oneTime.expiredHint)}</>}
        {status === 'cancelled' && <> {t($ => $.secrets.oneTime.cancelledHint)}</>}
      </p>
      {pending && !locked && <div className="form-field">
        <label htmlFor={`${identity}-value`}>{t($ => $.secrets.value)}</label>
        <input ref={input} id={`${identity}-value`} type="password" value={value} required disabled={!canEnter}
          autoComplete="new-password" autoCapitalize="none" spellCheck={false} aria-describedby={`${identity}-hint`}
          onChange={event => { setValue(event.target.value); setError(undefined); }} />
        <p className="form-hint" id={`${identity}-hint`}>{t($ => $.secrets.oneTime.valueHint)}</p>
      </div>}
      {pending && locked && !error && <p className="notice" role="status">
        {submitting ? t($ => $.secrets.oneTime.sending) : accepted ? t($ => $.secrets.oneTime.awaitingReceipt) : t($ => $.secrets.oneTime.submissionStarted)}
      </p>}
      {pending && error && <p className="notice" role="alert">{t($ => $.secrets.oneTime.errors[error.kind])}</p>}
      {readError && <p className="notice" role="alert">{t($ => $.secrets.oneTime.errors[readError.kind])}</p>}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={close}>{t($ => $.secrets.close)}</Button>
        {onOpenOperation && <Button variant="secondary" onClick={() => { close(); onOpenOperation(request.operation_id); }}>{t($ => $.secrets.oneTime.viewOperation)}</Button>}
        {(locked || !pending || readError) && <Button variant="secondary" disabled={reading} onClick={() => void inspect()}>
          {reading && <LoaderCircle size={16} className="connection-form-spinner" aria-hidden="true" />}
          {reading ? t($ => $.secrets.oneTime.checking) : t($ => $.secrets.oneTime.checkStatus)}
        </Button>}
        {pending && !locked && <Button type="submit" disabled={!canEnter || !value} aria-busy={submitting}>{t($ => $.secrets.oneTime.send)}</Button>}
      </div>
    </form>
  </Dialog>;
}

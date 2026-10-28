import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { LoaderCircle } from 'lucide-react';
import { createSecret, deleteSecret, getSecret, replaceSecret, SecretError, type SecretKey, type SecretMetadata } from '../api/secrets';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';

export type SecretDialogTarget =
  | { mode: 'create' }
  | { mode: 'provide'; key: SecretKey }
  | { mode: 'replace' | 'delete'; secret: SecretMetadata };

type Props = {
  connection: Connection;
  credentials: Session;
  target: SecretDialogTarget;
  onClose: () => void;
  onRefresh: () => void;
  onCompleted: (deleted: boolean) => void;
};

function localExpiry(secret?: SecretMetadata): string {
  if (!secret?.expires_at || secret.status === 'expired') return '';
  const date = new Date(secret.expires_at);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 19);
}

export function SecretDialog({ connection, credentials, target, onClose, onRefresh, onCompleted }: Props) {
  const { t } = useTranslation();
  const identity = useId();
  const initial = 'secret' in target ? target.secret : undefined;
  const key = target.mode === 'provide' ? target.key : initial;
  const [name, setName] = useState(key?.name ?? '');
  const [metadata, setMetadata] = useState(initial);
  const [value, setValue] = useState('');
  const [multiline, setMultiline] = useState(false);
  const [multilinePaste, setMultilinePaste] = useState(false);
  const [expiry, setExpiry] = useState(localExpiry(initial));
  const [loading, setLoading] = useState(target.mode === 'provide');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<SecretError>();
  const [invalidExpiry, setInvalidExpiry] = useState(false);
  const pending = useRef<AbortController | null>(null);
  const sent = useRef(false);
  const firstField = useRef<HTMLInputElement>(null);
  const multilineField = useRef<HTMLTextAreaElement>(null);
  const deleting = target.mode === 'delete';
  const blocked = loading || submitting || Boolean(error);

  useEffect(() => {
    firstField.current?.focus();
    return () => { pending.current?.abort(); pending.current = null; };
  }, [connection.url, credentials]);

  useEffect(() => {
    if (target.mode !== 'provide') return;
    const controller = new AbortController();
    pending.current = controller;
    void getSecret(connection.url, credentials, target.key, controller.signal).then(secret => {
      if (controller.signal.aborted) return;
      setMetadata(secret);
      setExpiry(localExpiry(secret));
    }).catch(cause => {
      if (controller.signal.aborted) return;
      // Only a confirmed missing key permits creation. Network and auth errors
      // must not turn a replacement into an unintended create attempt.
      if (!(cause instanceof SecretError && cause.kind === 'not_found')) {
        setError(cause instanceof SecretError ? cause : new SecretError('unavailable'));
      }
    }).finally(() => {
      if (controller.signal.aborted) return;
      pending.current = null;
      setLoading(false);
    });
    return () => controller.abort();
  }, [connection.url, credentials, target]);

  useEffect(() => { if (!loading) firstField.current?.focus(); }, [loading]);
  useEffect(() => { if (multiline) multilineField.current?.focus(); }, [multiline]);

  function close(refresh = Boolean(sent.current || error)) {
    setValue('');
    pending.current?.abort();
    pending.current = null;
    if (refresh) onRefresh();
    onClose();
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (sent.current || pending.current || blocked || !deleting && !value) return;
    const expiresAt = expiry ? new Date(expiry) : undefined;
    if (!deleting && expiresAt && (!Number.isFinite(expiresAt.getTime()) || expiresAt.getTime() <= Date.now())) {
      setInvalidExpiry(true);
      return;
    }
    const controller = new AbortController();
    pending.current = controller;
    sent.current = true;
    setSubmitting(true);
    setInvalidExpiry(false);
    const newValue = value;
    setValue('');
    try {
      if (deleting && metadata) {
        await deleteSecret(connection.url, credentials, metadata, controller.signal);
      } else {
        const input = { value: newValue, expires_at: expiresAt?.toISOString() };
        if (metadata) {
          await replaceSecret(connection.url, credentials, { name: metadata.name },
            { ...input, id: metadata.id, version: metadata.version }, controller.signal);
        } else {
          await createSecret(connection.url, credentials, { ...input, name }, controller.signal);
        }
      }
      if (!controller.signal.aborted && pending.current === controller) onCompleted(deleting);
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) setError(cause instanceof SecretError ? cause : new SecretError('unknown'));
    } finally {
      if (!controller.signal.aborted && pending.current === controller) { pending.current = null; setSubmitting(false); }
    }
  }

  const title = deleting ? t($ => $.secrets.deleteTitle) : target.mode === 'replace' ? t($ => $.secrets.replaceTitle)
    : target.mode === 'provide' ? t($ => $.secrets.provideTitle) : t($ => $.secrets.addTitle);

  return <Dialog open title={title} description={deleting ? t($ => $.secrets.deleteDescription) : undefined} onClose={() => close()}>
    <form className="secret-form" onSubmit={submit} autoComplete="off">
      {target.mode === 'create' ? <>
        <div className="form-field"><label htmlFor={`${identity}-name`}>{t($ => $.secrets.name)}</label>
          <input ref={firstField} id={`${identity}-name`} value={name} required pattern={'[A-Za-z0-9_][A-Za-z0-9_.\\-]*'} maxLength={128}
            disabled={blocked} autoCapitalize="none" spellCheck={false} onChange={event => setName(event.target.value)} /></div>
      </> : <p className="secret-key"><strong>{name}</strong></p>}
      {loading && <p className="catalog-loading" role="status"><LoaderCircle size={16} className="connection-form-spinner" aria-hidden="true" />{t($ => $.secrets.loadingMetadata)}</p>}
      {!deleting && <>
        <div className="form-field"><label htmlFor={`${identity}-value`}>{t($ => $.secrets.value)}</label>
          {multiline ? <textarea ref={multilineField} id={`${identity}-value`} value={value} required rows={5} autoComplete="off" autoCapitalize="none"
            disabled={blocked} spellCheck={false} aria-describedby={`${identity}-value-hint`} onChange={event => setValue(event.target.value)} />
            : <input ref={target.mode !== 'create' ? firstField : undefined} id={`${identity}-value`} type="password" value={value} required autoComplete="new-password"
              disabled={blocked} spellCheck={false} aria-describedby={`${identity}-value-hint`} onChange={event => { setValue(event.target.value); setMultilinePaste(false); }}
              onPaste={event => {
                if (/[\r\n]/.test(event.clipboardData.getData('text'))) {
                  event.preventDefault();
                  setMultilinePaste(true);
                }
              }} />}
          <p id={`${identity}-value-hint`} className="form-hint">{t($ => $.secrets.valueHint)}{multiline && <> {t($ => $.secrets.multilineHint)}</>}</p>
          {multilinePaste && <p className="form-error" role="alert">{t($ => $.secrets.multilinePaste)}</p>}
          {!multiline && <Button variant="secondary" className="secret-multiline" disabled={blocked} onClick={() => { setMultiline(true); setMultilinePaste(false); }}>{t($ => $.secrets.multiline)}</Button>}
        </div>
        <div className="form-field"><label htmlFor={`${identity}-expiry`}>{t($ => $.secrets.expiresAt)}</label>
          <input id={`${identity}-expiry`} type="datetime-local" step="1" value={expiry} disabled={blocked} aria-invalid={invalidExpiry}
            aria-describedby={`${identity}-expiry-hint`} onChange={event => { setExpiry(event.target.value); setInvalidExpiry(false); }} />
          <p id={`${identity}-expiry-hint`} className="form-hint">{t($ => $.secrets.expiryHint)}</p>
          {invalidExpiry && <p className="form-error" role="alert">{t($ => $.secrets.invalidExpiry)}</p>}
        </div>
      </>}
      {error && <div className="notice" role="alert"><p>{t($ => $.secrets.errors[error.kind])}</p>
        {sent.current && !['conflict', 'unknown'].includes(error.kind) && <p>{t($ => $.secrets.writeFailure)}</p>}
      </div>}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={() => close(Boolean(error || sent.current))}>{error ? t($ => $.secrets.inspect) : t($ => $.secrets.cancel)}</Button>
        {!error && <Button type="submit" className={deleting ? 'danger-button' : undefined} disabled={blocked || !deleting && !value} aria-busy={submitting}>
          {submitting && <LoaderCircle size={16} className="connection-form-spinner" aria-hidden="true" />}
          {submitting ? deleting ? t($ => $.secrets.deleting) : t($ => $.secrets.saving) : deleting ? t($ => $.secrets.deleteSubmit)
            : target.mode === 'replace' ? t($ => $.secrets.replaceSubmit) : t($ => $.secrets.save)}
        </Button>}
      </div>
    </form>
  </Dialog>;
}

import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { LoaderCircle } from 'lucide-react';
import { normalizeURL } from '../connections/client';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';

type ConnectionValues = { name: string; url: string; username: string; password: string };
type Field = keyof ConnectionValues;

export function ConnectionDialog({ connection, credentials, defaultURL = 'http://localhost:4274', onClose, onConnect }: {
  connection?: Connection;
  credentials?: Session;
  defaultURL?: string;
  onClose: () => void;
  onConnect: (value: ConnectionValues, signal: AbortSignal) => Promise<string | void>;
}) {
  const { t } = useTranslation();
  const identity = useId();
  const errorID = `${identity}-error`;
  const hintID = `${identity}-credentials-hint`;
  const urlHintID = `${identity}-url-hint`;
  const [values, setValues] = useState<ConnectionValues>(() => ({
    name: connection?.name ?? t($ => $.connectionForm.defaultName),
    url: connection?.url ?? defaultURL,
    username: credentials?.username ?? (connection ? '' : 'commander'),
    password: '',
  }));
  const [error, setError] = useState('');
  const [invalidField, setInvalidField] = useState<Field>();
  const [submitting, setSubmitting] = useState(false);
  const pending = useRef<AbortController | null>(null);

  useEffect(() => () => pending.current?.abort(), []);
  useEffect(() => {
    document.getElementById(`${identity}-${connection ? 'name' : 'password'}`)?.focus();
  }, [identity, connection]);

  function close() {
    pending.current?.abort();
    onClose();
  }

  function update(field: Field, value: string) {
    setValues(previous => ({ ...previous, [field]: value }));
    if (!invalidField || invalidField === field) {
      setError('');
      setInvalidField(undefined);
    }
  }

  function invalid(field: Field, message: string) {
    setInvalidField(field);
    setError(message);
    document.getElementById(`${identity}-${field}`)?.focus();
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current) return;
    setError('');
    setInvalidField(undefined);
    const value = {
      name: values.name.trim(),
      url: values.url.trim(),
      username: values.username,
      password: values.password,
    };

    if (!value.name) return invalid('name', t($ => $.connectionForm.invalidName));
    try {
      normalizeURL(value.url);
    } catch {
      return invalid('url', t($ => $.connectionForm.invalidURL));
    }
    if (!value.username.trim() || /[:\u0000-\u001f\u007f-\u009f]/u.test(value.username)) {
      return invalid('username', t($ => $.connectionForm.invalidUsername));
    }
    if (!value.password.trim() || /[\u0000-\u001f\u007f-\u009f]/u.test(value.password)) {
      return invalid('password', t($ => $.connectionForm.invalidPassword));
    }

    const controller = new AbortController();
    pending.current = controller;
    setSubmitting(true);
    try {
      const message = await onConnect(value, controller.signal);
      if (!controller.signal.aborted && pending.current === controller && message) setError(message);
    } catch {
      if (!controller.signal.aborted && pending.current === controller) {
        setError(t($ => $.connectionForm.connectionFailed));
      }
    } finally {
      if (!controller.signal.aborted && pending.current === controller) {
        pending.current = null;
        setSubmitting(false);
      }
    }
  }

  function description(field: Field) {
    return [field === 'password' ? hintID : field === 'url' ? urlHintID : '', invalidField === field ? errorID : ''].filter(Boolean).join(' ') || undefined;
  }

  return <Dialog open title={connection ? t($ => $.connectionForm.editTitle) : t($ => $.connectionForm.addTitle)} onClose={close}>
    <form className="connection-form" noValidate autoComplete="off" onSubmit={submit}>
      <div className="form-field">
        <label htmlFor={`${identity}-name`}>{t($ => $.connectionForm.name)}</label>
        <input id={`${identity}-name`} name="name" type="text" required maxLength={120} value={values.name}
          disabled={submitting} aria-invalid={invalidField === 'name' || undefined} aria-describedby={description('name')}
          onChange={event => update('name', event.target.value)} />
      </div>
      <div className="form-field">
        <label htmlFor={`${identity}-url`}>{t($ => $.connectionForm.url)}</label>
        <input id={`${identity}-url`} name="url" type="url" required maxLength={2048} value={values.url} spellCheck={false} autoCapitalize="none"
          disabled={submitting} aria-invalid={invalidField === 'url' || undefined} aria-describedby={description('url')}
          onChange={event => update('url', event.target.value)} />
        <p className="form-hint" id={urlHintID}>{t($ => $.connectionForm.urlHint)}</p>
      </div>
      <div className="form-field">
        <label htmlFor={`${identity}-username`}>{t($ => $.connectionForm.username)}</label>
        <input id={`${identity}-username`} name="username" type="text" required maxLength={256} value={values.username} spellCheck={false} autoCapitalize="none"
          disabled={submitting} aria-invalid={invalidField === 'username' || undefined} aria-describedby={description('username')}
          onChange={event => update('username', event.target.value)} />
      </div>
      <div className="form-field">
        <label htmlFor={`${identity}-password`}>{t($ => $.connectionForm.password)}</label>
        <input id={`${identity}-password`} name="password" type="password" required maxLength={4096} value={values.password}
          disabled={submitting} aria-invalid={invalidField === 'password' || undefined} aria-describedby={description('password')}
          onChange={event => update('password', event.target.value)} />
      </div>
      <p className="form-hint" id={hintID}>{t($ => $.connectionForm.credentialsHint)}</p>
      {error && <p className="form-error" id={errorID} role="alert">{error}</p>}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={close}>{t($ => $.connectionForm.cancel)}</Button>
        <Button type="submit" disabled={submitting} aria-busy={submitting}>
          {submitting && <LoaderCircle className="connection-form-spinner" size={16} aria-hidden="true" />}
          <span>{submitting ? t($ => $.connectionForm.testing) : connection ? t($ => $.connectionForm.save) : t($ => $.connectionForm.connect)}</span>
        </Button>
      </div>
    </form>
  </Dialog>;
}

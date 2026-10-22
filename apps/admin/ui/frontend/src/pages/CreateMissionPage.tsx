import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { ArrowLeft, LoaderCircle } from 'lucide-react';
import { createMission, MissionCreateError } from '../api/missions';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from '../components/Button';
import '../styles/missions.css';

const initialDefinition = JSON.stringify({
  name: '',
  objectives: [''],
  required_actions: [],
  starting_actions: [],
  input_schema: {},
  output_schema: {},
}, null, 2);

type Props = {
  connection: Connection;
  credentials?: Session;
  onSignIn: () => void;
  onBack: () => void;
  onSelect: (name: string) => void;
};

export function CreateMissionPage(props: Props) {
  const { t } = useTranslation();
  const { connection, credentials, onSignIn, onBack } = props;
  const [owner, setOwner] = useState({ connectionID: connection.id, url: connection.url, credentials, revision: 0 });
  // Remount before a different connection or session can reuse the draft.
  if (owner.connectionID !== connection.id || owner.url !== connection.url || owner.credentials !== credentials) {
    setOwner({ connectionID: connection.id, url: connection.url, credentials, revision: owner.revision + 1 });
  }

  return <section className="missions-page mission-create-page">
    <button className="missions-back" type="button" onClick={onBack}>
      <ArrowLeft size={16} aria-hidden="true" />{t($ => $.missions.back)}
    </button>
    <div className="page-heading"><div><h1>{t($ => $.missions.createTitle)}</h1>
      <p>{t($ => $.missions.createDescription)}</p>
    </div></div>
    {credentials ? <CreateMissionForm key={owner.revision} {...props} credentials={credentials} />
      : <div className="notice" role="alert"><p>{t($ => $.missions.createSignIn)}</p>
        <Button onClick={onSignIn}>{t($ => $.missions.signIn)}</Button>
      </div>}
  </section>;
}

function CreateMissionForm({ connection, credentials, onSignIn, onBack, onSelect }: Props & { credentials: Session }) {
  const { t } = useTranslation();
  const identity = useId();
  const editor = useRef<HTMLTextAreaElement>(null);
  const pending = useRef<AbortController | null>(null);
  const [definition, setDefinition] = useState(initialDefinition);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<MissionCreateError>();
  const [submittedName, setSubmittedName] = useState<string>();
  const locked = error?.kind === 'unknown';
  const invalid = error?.kind === 'invalid_json' || error?.kind === 'invalid_input';

  useEffect(() => {
    editor.current?.focus();
    return () => pending.current?.abort();
  }, [connection.url, credentials]);

  useEffect(() => { if (invalid && !submitting) editor.current?.focus(); }, [invalid, submitting]);

  function close() {
    pending.current?.abort();
    onBack();
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current || locked) return;
    const controller = new AbortController();
    pending.current = controller;
    setSubmitting(true);
    setError(undefined);
    // Keep the submitted text intact; parsing and reserializing would round
    // large numeric constants in JSON schemas before they reach the API.
    try {
      const parsed: unknown = JSON.parse(definition);
      setSubmittedName(typeof parsed === 'object' && parsed !== null && 'name' in parsed && typeof parsed.name === 'string' ? parsed.name : undefined);
    } catch { setSubmittedName(undefined); }
    try {
      const mission = await createMission(connection.url, credentials, definition, controller.signal);
      if (!controller.signal.aborted && pending.current === controller) onSelect(mission.name);
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) {
        const failure = cause instanceof MissionCreateError ? cause : new MissionCreateError('unknown');
        setError(failure);
      }
    } finally {
      if (!controller.signal.aborted && pending.current === controller) {
        pending.current = null;
        setSubmitting(false);
      }
    }
  }

  return <form className="mission-create-form" onSubmit={submit} autoComplete="off">
      <div className="form-field">
        <label htmlFor={`${identity}-definition`}>{t($ => $.missions.definition)}</label>
        <textarea ref={editor} id={`${identity}-definition`} rows={20} value={definition} spellCheck={false}
          autoCapitalize="none" disabled={submitting || locked} aria-invalid={invalid || undefined}
          aria-describedby={`${identity}-hint${error ? ` ${identity}-error` : ''}`}
          onChange={event => { setDefinition(event.target.value); setError(undefined); }} />
      </div>
      <p className="form-hint" id={`${identity}-hint`}>{t($ => $.missions.definitionHint)}</p>
      {error && <div className="notice" id={`${identity}-error`} role="alert">
        <p>{t($ => $.missions.createErrors[error.kind])}</p>
        {error.kind === 'authentication' && <Button onClick={onSignIn}>{t($ => $.missions.signIn)}</Button>}
        {submittedName && (locked || error.kind === 'conflict') && <Button variant="secondary" onClick={() => onSelect(submittedName)}>
          {locked ? t($ => $.missions.checkSaved) : t($ => $.missions.viewExisting)}
        </Button>}
      </div>}
      {submitting && <p className="form-hint" role="status">{t($ => $.missions.creating)}</p>}
      <div className="mission-create-actions">
        <Button variant="secondary" onClick={close}>{t($ => $.missions.cancel)}</Button>
        <Button type="submit" disabled={submitting || locked} aria-busy={submitting}>
          {submitting && <LoaderCircle className="connection-form-spinner" size={16} aria-hidden="true" />}
          {t($ => $.missions.createTitle)}
        </Button>
      </div>
    </form>;
}

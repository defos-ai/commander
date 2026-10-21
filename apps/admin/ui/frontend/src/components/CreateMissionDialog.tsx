import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { LoaderCircle } from 'lucide-react';
import { createMission, MissionCreateError } from '../api/missions';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';

const initialDefinition = JSON.stringify({
  name: '',
  objective: '',
  required_actions: [],
  starting_actions: [],
  input_schema: {},
  output_schema: {},
}, null, 2);

export function CreateMissionDialog({ connection, credentials, onClose, onCreated, onInspect }: {
  connection: Connection;
  credentials: Session;
  onClose: () => void;
  onCreated: (name: string) => void;
  onInspect: (name: string) => void;
}) {
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
    onClose();
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
      if (!controller.signal.aborted && pending.current === controller) onCreated(mission.name);
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

  return <Dialog open title={t($ => $.missions.createTitle)} description={t($ => $.missions.createDescription)}
    onClose={close} className="mission-create-dialog">
    <form className="mission-create-form" onSubmit={submit} autoComplete="off">
      <div className="form-field">
        <label htmlFor={`${identity}-definition`}>{t($ => $.missions.definition)}</label>
        <textarea ref={editor} id={`${identity}-definition`} rows={16} value={definition} spellCheck={false}
          autoCapitalize="none" disabled={submitting || locked} aria-invalid={invalid || undefined}
          aria-describedby={`${identity}-hint${error ? ` ${identity}-error` : ''}`}
          onChange={event => { setDefinition(event.target.value); setError(undefined); }} />
      </div>
      <p className="form-hint" id={`${identity}-hint`}>{t($ => $.missions.definitionHint)}</p>
      {error && <div className="notice" id={`${identity}-error`} role="alert">
        <p>{t($ => $.missions.createErrors[error.kind])}</p>
        {submittedName && (locked || error.kind === 'conflict') && <Button variant="secondary" onClick={() => onInspect(submittedName)}>
          {locked ? t($ => $.missions.checkSaved) : t($ => $.missions.viewExisting)}
        </Button>}
      </div>}
      {submitting && <p className="form-hint" role="status">{t($ => $.missions.creating)}</p>}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={close}>{t($ => $.missions.cancel)}</Button>
        <Button type="submit" disabled={submitting || locked} aria-busy={submitting}>
          {submitting && <LoaderCircle className="connection-form-spinner" size={16} aria-hidden="true" />}
          {t($ => $.missions.createTitle)}
        </Button>
      </div>
    </form>
  </Dialog>;
}

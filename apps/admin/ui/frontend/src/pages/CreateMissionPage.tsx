import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { ArrowLeft, LoaderCircle } from 'lucide-react';
import { createMission, MissionCreateError } from '../api/missions';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from '../components/Button';
import { MissionFormFields } from '../components/MissionFormFields';
import { initialMissionDefinition, readMissionDraft, writeMissionDraft, type MissionDraft } from '../missions/draft';
import '../styles/missions.css';

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
  const form = useRef<HTMLFormElement>(null);
  const nameInput = useRef<HTMLInputElement>(null);
  const editor = useRef<HTMLTextAreaElement>(null);
  const pending = useRef<AbortController | null>(null);
  const [definition, setDefinition] = useState(initialMissionDefinition);
  const [draft, setDraft] = useState<MissionDraft>(() => readMissionDraft(initialMissionDefinition));
  const [mode, setMode] = useState<'form' | 'json'>('form');
  const [draftError, setDraftError] = useState<'unsupportedJSON' | 'invalidFields'>();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<MissionCreateError>();
  const [submittedName, setSubmittedName] = useState<string>();
  const locked = error?.kind === 'unknown';
  const invalid = error?.kind === 'invalid_json' || error?.kind === 'invalid_input';

  useEffect(() => () => pending.current?.abort(), []);

  useEffect(() => { (mode === 'form' ? nameInput.current : editor.current)?.focus(); }, [mode]);

  useEffect(() => {
    if (invalid && !submitting) (mode === 'form' ? nameInput.current : editor.current)?.focus();
  }, [invalid, submitting, mode]);

  function formDefinition(): string | undefined {
    try { return writeMissionDraft(definition, draft); }
    catch {
      setDraftError('invalidFields');
      const advanced = form.current?.querySelector<HTMLDetailsElement>('.mission-advanced');
      if (advanced) { advanced.open = true; advanced.querySelector('summary')?.focus(); }
      return undefined;
    }
  }

  function switchMode(next: 'form' | 'json') {
    if (next === mode || submitting || locked) return;
    if (next === 'form') {
      try { setDraft(readMissionDraft(definition)); }
      catch { setDraftError('unsupportedJSON'); return; }
    } else {
      const source = formDefinition();
      if (source === undefined) return;
      setDefinition(source);
    }
    setDraftError(undefined);
    setError(undefined);
    setMode(next);
  }

  function close() {
    pending.current?.abort();
    onBack();
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current || locked) return;
    const source = mode === 'form' ? formDefinition() : definition;
    if (source === undefined) return;
    setDefinition(source);
    const controller = new AbortController();
    pending.current = controller;
    setSubmitting(true);
    setDraftError(undefined);
    setError(undefined);
    // Keep the submitted text intact; parsing and reserializing would round
    // large numeric constants in JSON schemas before they reach the API.
    try {
      const parsed: unknown = JSON.parse(source);
      setSubmittedName(typeof parsed === 'object' && parsed !== null && 'name' in parsed && typeof parsed.name === 'string' ? parsed.name : undefined);
    } catch { setSubmittedName(undefined); }
    try {
      const mission = await createMission(connection.url, credentials, source, controller.signal);
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

  return <form ref={form} className="mission-create-form" onSubmit={submit} autoComplete="off">
      <div className="mission-view-switch" role="group" aria-label={t($ => $.missions.builder.editorLabel)}>
        <button type="button" aria-pressed={mode === 'form'} disabled={submitting || locked} onClick={() => switchMode('form')}>
          {t($ => $.missions.builder.form)}
        </button>
        <button type="button" aria-pressed={mode === 'json'} disabled={submitting || locked} onClick={() => switchMode('json')}>
          {t($ => $.missions.json)}
        </button>
      </div>
      {draftError && <div className="notice" role="alert"><p>{t($ => $.missions.builder[draftError])}</p></div>}
      {mode === 'form' ? <MissionFormFields draft={draft} onChange={value => { setDraft(value); setDraftError(undefined); setError(undefined); }}
        connection={connection} credentials={credentials} disabled={submitting || locked} nameInput={nameInput} onSignIn={onSignIn} />
        : <div className="form-field">
        <label htmlFor={`${identity}-definition`}>{t($ => $.missions.definition)}</label>
        <textarea ref={editor} id={`${identity}-definition`} className="mission-definition-editor" data-initial-focus rows={20} value={definition} spellCheck={false}
          autoCapitalize="none" disabled={submitting || locked} aria-invalid={invalid || undefined}
          aria-describedby={`${identity}-hint${error ? ` ${identity}-error` : ''}`}
          onChange={event => { setDefinition(event.target.value); setDraftError(undefined); setError(undefined); }} />
      </div>}
      <p className="form-hint" id={`${identity}-hint`}>{mode === 'form'
        ? t($ => $.missions.builder.saveHint) : t($ => $.missions.definitionHint)}</p>
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

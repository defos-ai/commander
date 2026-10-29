import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { ArrowLeft, LoaderCircle } from 'lucide-react';
import { updateMission, MissionUpdateError } from '../api/missions';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { useMissionQuery } from '../hooks/useMissionQuery';
import { Button } from '../components/Button';
import { MissionFormFields } from '../components/MissionFormFields';
import { readMissionDraft, writeMissionDraft, type MissionDraft } from '../missions/draft';
import { formatJSON } from '../missions/format';
import '../styles/missions.css';

type Props = {
  connection: Connection;
  credentials?: Session;
  name: string;
  onSignIn: () => void;
  onBack: () => void;
  onSelect: (name: string) => void;
};

export function EditMissionPage({ connection, credentials, name, onSignIn, onBack, onSelect }: Props) {
  const { t } = useTranslation();
  const query = useMissionQuery(connection.url, credentials, name);
  const definition = query.content && 'definition' in query.content ? query.content.definition : undefined;
  const error = !credentials ? 'authentication' : query.error?.kind;

  return <section className="missions-page mission-edit-page">
    <button className="missions-back" type="button" onClick={onBack}>
      <ArrowLeft size={16} aria-hidden="true" />{t($ => $.missions.backToMission)}
    </button>
    <div className="page-heading"><div><h1>{t($ => $.missions.editTitle)}</h1>
      <p>{t($ => $.missions.editDescription)}</p>
    </div></div>
    {error ? <div className="notice" role="alert">
      <p>{error === 'authentication' ? t($ => $.missions.editSignIn) : t($ => $.missions.errors[error])}</p>
      {error === 'authentication' ? <Button onClick={onSignIn}>{t($ => $.missions.signIn)}</Button>
        : error !== 'not_found' && <Button variant="secondary" onClick={query.refresh}>{t($ => $.missions.retry)}</Button>}
    </div> : query.loading ? <p className="missions-loading" role="status">
      <LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />{t($ => $.missions.loadingDetail)}
    </p> : credentials && definition !== undefined && <EditMissionForm key={`${connection.id}:${connection.url}:${name}`}
      connection={connection} credentials={credentials} name={name} initialDefinition={definition}
      onSignIn={onSignIn} onBack={onBack} onSelect={onSelect} />}
  </section>;
}

function EditMissionForm({ connection, credentials, name, initialDefinition, onSignIn, onBack, onSelect }: Props & { credentials: Session; initialDefinition: string }) {
  const { t } = useTranslation();
  const identity = useId();
  const form = useRef<HTMLFormElement>(null);
  const nameInput = useRef<HTMLInputElement>(null);
  const editor = useRef<HTMLTextAreaElement>(null);
  const pending = useRef<AbortController | null>(null);
  const [definition, setDefinition] = useState(() => formatJSON(initialDefinition));
  const [draft, setDraft] = useState<MissionDraft>(() => formattedDraft(initialDefinition));
  const [mode, setMode] = useState<'form' | 'json'>('form');
  const [draftError, setDraftError] = useState<'unsupportedJSON' | 'invalidFields'>();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<MissionUpdateError>();
  const locked = error?.kind === 'unknown';
  const invalid = error?.kind === 'invalid_json' || error?.kind === 'invalid_input' || error?.kind === 'name_changed';

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
      try {
        const nextDraft = formattedDraft(definition);
        if (nextDraft.name !== name) { setError(new MissionUpdateError('name_changed')); return; }
        setDraft(nextDraft);
      } catch { setDraftError('unsupportedJSON'); return; }
    } else {
      const source = formDefinition();
      if (source === undefined) return;
      setDefinition(formatJSON(source));
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
    try {
      const saved = await updateMission(connection.url, credentials, name, source, controller.signal);
      if (!controller.signal.aborted && pending.current === controller) onSelect(saved.name);
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) {
        setError(cause instanceof MissionUpdateError ? cause : new MissionUpdateError('unknown'));
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
      connection={connection} credentials={credentials} disabled={submitting || locked} nameInput={nameInput} nameReadOnly onSignIn={onSignIn} />
      : <div className="form-field">
        <label htmlFor={`${identity}-definition`}>{t($ => $.missions.definition)}</label>
        <textarea ref={editor} id={`${identity}-definition`} className="mission-definition-editor" data-initial-focus rows={20} value={definition} spellCheck={false}
          autoCapitalize="none" disabled={submitting || locked} aria-invalid={invalid || undefined}
          aria-describedby={`${identity}-hint${error ? ` ${identity}-error` : ''}`}
          onChange={event => { setDefinition(event.target.value); setDraftError(undefined); setError(undefined); }} />
      </div>}
    <p className="form-hint" id={`${identity}-hint`}>{mode === 'form' ? t($ => $.missions.editHint) : t($ => $.missions.editDefinitionHint)}</p>
    {error && <div className="notice" id={`${identity}-error`} role="alert">
      <p>{t($ => $.missions.updateErrors[error.kind])}</p>
      {error.kind === 'authentication' && <Button onClick={onSignIn}>{t($ => $.missions.signIn)}</Button>}
      {(locked || error.kind === 'conflict') && <Button variant="secondary" onClick={() => onSelect(name)}>
        {t($ => $.missions.checkSaved)}
      </Button>}
    </div>}
    {submitting && <p className="form-hint" role="status">{t($ => $.missions.creating)}</p>}
    <div className="mission-create-actions">
      <Button variant="secondary" onClick={close}>{t($ => $.missions.cancel)}</Button>
      <Button type="submit" disabled={submitting || locked} aria-busy={submitting}>
        {submitting && <LoaderCircle className="connection-form-spinner" size={16} aria-hidden="true" />}
        {t($ => $.missions.saveChanges)}
      </Button>
    </div>
  </form>;
}

function formattedDraft(source: string): MissionDraft {
  const draft = readMissionDraft(source);
  return {
    ...draft,
    input_schema: formatJSON(draft.input_schema),
    output_schema: formatJSON(draft.output_schema),
    rules: formatJSON(draft.rules),
    action_definitions: formatJSON(draft.action_definitions),
  };
}

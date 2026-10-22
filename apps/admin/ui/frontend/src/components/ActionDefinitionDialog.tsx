import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from '../i18n';
import { ActionDefinitionDraftError, validateActionDefinition, type ActionDefinitionDraft } from '../missions/action-definitions';
import { Button } from './Button';
import { Dialog } from './Dialog';

type Props = {
  definition?: ActionDefinitionDraft;
  onClose: () => void;
  onSave: (draft: ActionDefinitionDraft) => void;
};

const initialSchema = '{\n  "type": "object",\n  "properties": {}\n}';

export function ActionDefinitionDialog({ definition, onClose, onSave }: Props) {
  const { t } = useTranslation();
  const identity = useId();
  const [name, setName] = useState(definition?.name ?? '');
  const [description, setDescription] = useState(definition?.description ?? '');
  const [inputSchema, setInputSchema] = useState(definition?.input_schema ?? initialSchema);
  const [outputSchema, setOutputSchema] = useState(definition?.output_schema ?? initialSchema);
  const [secretNames, setSecretNames] = useState(definition?.required_secrets.join('\n') ?? '');
  const [error, setError] = useState<ActionDefinitionDraftError['kind']>();
  const nameField = useRef<HTMLInputElement>(null);
  const descriptionField = useRef<HTMLTextAreaElement>(null);
  const inputSchemaField = useRef<HTMLTextAreaElement>(null);
  const outputSchemaField = useRef<HTMLTextAreaElement>(null);
  const secretsField = useRef<HTMLTextAreaElement>(null);
  const errorMessage = useRef<HTMLParagraphElement>(null);
  const editing = Boolean(definition);

  useEffect(() => { (editing ? descriptionField : nameField).current?.focus(); }, [editing]);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    // A portal removes the nested form from the DOM, but React submit events
    // still bubble through its owner, which is the mission creation form.
    event.stopPropagation();
    const draft: ActionDefinitionDraft = {
      name: name.trim(),
      description,
      input_schema: inputSchema,
      output_schema: outputSchema,
      required_secrets: secretNames.split(/\r?\n/).map(value => value.trim()).filter(Boolean),
    };
    try {
      validateActionDefinition(draft);
      onSave(draft);
    } catch (cause) {
      const kind = cause instanceof ActionDefinitionDraftError ? cause.kind : 'invalid_definitions';
      setError(kind);
      const fields = {
        name: nameField,
        duplicate: nameField,
        description: descriptionField,
        input_schema: inputSchemaField,
        output_schema: outputSchemaField,
        required_secrets: secretsField,
      };
      if (kind in fields) fields[kind as keyof typeof fields].current?.focus();
      else requestAnimationFrame(() => errorMessage.current?.focus());
    }
  }

  const validationID = error ? `${identity}-validation` : undefined;
  return createPortal(<Dialog open className="mission-action-dialog" title={editing ? t($ => $.missions.actionDefinition.editTitle) : t($ => $.missions.actionDefinition.createTitle)}
    description={t($ => $.missions.actionDefinition.description)} onClose={onClose}>
    <form className="mission-action-definition-form" noValidate autoComplete="off" onSubmit={submit}
      onKeyDown={event => { if (event.key === 'Escape') event.stopPropagation(); }}>
      <div className="form-field">
        <label htmlFor={`${identity}-name`}>{t($ => $.missions.actionDefinition.name)}</label>
        <input ref={nameField} id={`${identity}-name`} value={name} required readOnly={editing} autoCapitalize="none" spellCheck={false}
          aria-invalid={error === 'name' || error === 'duplicate' || undefined}
          aria-describedby={`${identity}-name-hint${error === 'name' || error === 'duplicate' ? ` ${validationID}` : ''}`}
          onChange={event => { setName(event.target.value); setError(undefined); }} />
        <p id={`${identity}-name-hint`} className="form-hint">{t($ => $.missions.actionDefinition.nameHint)}</p>
      </div>
      <div className="form-field">
        <label htmlFor={`${identity}-description`}>{t($ => $.missions.actionDefinition.descriptionLabel)}</label>
        <textarea ref={descriptionField} id={`${identity}-description`} rows={3} value={description}
          aria-invalid={error === 'description' || undefined}
          aria-describedby={`${identity}-description-hint${error === 'description' ? ` ${validationID}` : ''}`}
          onChange={event => { setDescription(event.target.value); setError(undefined); }} />
        <p id={`${identity}-description-hint`} className="form-hint">{t($ => $.missions.actionDefinition.descriptionHint)}</p>
      </div>
      <div className="form-field mission-action-definition-schema">
        <label htmlFor={`${identity}-input`}>{t($ => $.missions.actionDefinition.inputSchema)}</label>
        <textarea ref={inputSchemaField} id={`${identity}-input`} rows={6} value={inputSchema} spellCheck={false} autoCapitalize="none"
          aria-invalid={error === 'input_schema' || undefined} aria-describedby={error === 'input_schema' ? validationID : undefined}
          onChange={event => { setInputSchema(event.target.value); setError(undefined); }} />
      </div>
      <div className="form-field mission-action-definition-schema">
        <label htmlFor={`${identity}-output`}>{t($ => $.missions.actionDefinition.outputSchema)}</label>
        <textarea ref={outputSchemaField} id={`${identity}-output`} rows={6} value={outputSchema} spellCheck={false} autoCapitalize="none"
          aria-invalid={error === 'output_schema' || undefined} aria-describedby={error === 'output_schema' ? validationID : undefined}
          onChange={event => { setOutputSchema(event.target.value); setError(undefined); }} />
      </div>
      <div className="form-field">
        <label htmlFor={`${identity}-secrets`}>{t($ => $.missions.actionDefinition.secrets)}</label>
        <textarea ref={secretsField} id={`${identity}-secrets`} rows={3} value={secretNames} spellCheck={false} autoCapitalize="none"
          aria-invalid={error === 'required_secrets' || undefined}
          aria-describedby={`${identity}-secrets-hint${error === 'required_secrets' ? ` ${validationID}` : ''}`}
          onChange={event => { setSecretNames(event.target.value); setError(undefined); }} />
        <p id={`${identity}-secrets-hint`} className="form-hint">{t($ => $.missions.actionDefinition.secretsHint)}</p>
      </div>
      {error && <p ref={errorMessage} id={`${identity}-validation`} className="form-error" role="alert" tabIndex={-1}>{t($ => $.missions.actionDefinition.errors[error])}</p>}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={onClose}>{t($ => $.missions.actionDefinition.cancel)}</Button>
        <Button type="submit">{editing ? t($ => $.missions.actionDefinition.save) : t($ => $.missions.actionDefinition.add)}</Button>
      </div>
    </form>
  </Dialog>, document.body);
}

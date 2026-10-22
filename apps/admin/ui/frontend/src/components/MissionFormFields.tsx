import { useId, type RefObject } from 'react';
import type { Connection, Session } from '../connections/types';
import type { MissionDraft } from '../missions/draft';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { MissionActionPicker } from './MissionActionPicker';

type Props = {
  draft: MissionDraft;
  onChange: (draft: MissionDraft) => void;
  connection: Connection;
  credentials: Session;
  disabled: boolean;
  nameInput: RefObject<HTMLInputElement | null>;
  onSignIn: () => void;
};

export function MissionFormFields({ draft, onChange, connection, credentials, disabled, nameInput, onSignIn }: Props) {
  const { t } = useTranslation();
  const identity = useId();
  const advanced = [
    { field: 'input_schema', label: t($ => $.missions.builder.inputSchema) },
    { field: 'output_schema', label: t($ => $.missions.builder.outputSchema) },
    { field: 'rules', label: t($ => $.missions.builder.rules) },
    { field: 'steps', label: t($ => $.missions.builder.steps) },
    { field: 'action_definitions', label: t($ => $.missions.builder.actionDefinitions) },
  ] as const;

  return <>
    <div className="form-field">
      <label htmlFor={`${identity}-name`}>{t($ => $.missions.builder.name)}</label>
      <input ref={nameInput} id={`${identity}-name`} data-initial-focus value={draft.name} required disabled={disabled}
        placeholder={t($ => $.missions.builder.namePlaceholder)} autoCapitalize="none" spellCheck={false}
        onChange={event => onChange({ ...draft, name: event.target.value })} />
    </div>
    <section className="mission-objectives" aria-labelledby={`${identity}-objectives`}>
      <h2 id={`${identity}-objectives`}>{t($ => $.missions.objectives)}</h2>
      {draft.objectives.map((objective, index) => <div className="mission-objective" key={index}>
        <div className="form-field">
          <label htmlFor={`${identity}-objective-${index}`}>{t($ => $.missions.builder.objective, { number: index + 1 })}</label>
          <textarea id={`${identity}-objective-${index}`} className="mission-objective-input" rows={2}
            value={objective} required disabled={disabled} placeholder={t($ => $.missions.builder.objectivePlaceholder)}
            onChange={event => onChange({ ...draft, objectives: draft.objectives.map((value, position) => position === index ? event.target.value : value) })} />
        </div>
        <Button variant="secondary" disabled={disabled || draft.objectives.length === 1}
          onClick={() => onChange({ ...draft, objectives: draft.objectives.filter((_, position) => position !== index) })}>
          {t($ => $.missions.builder.removeObjective, { number: index + 1 })}
        </Button>
      </div>)}
      <Button variant="secondary" disabled={disabled} onClick={() => onChange({ ...draft, objectives: [...draft.objectives, ''] })}>
        {t($ => $.missions.builder.addObjective)}
      </Button>
    </section>
    <MissionActionPicker connection={connection} credentials={credentials} disabled={disabled} onSignIn={onSignIn}
      requiredActions={draft.required_actions} optionalActions={draft.optional_actions} startingActions={draft.starting_actions}
      onChange={selection => onChange({ ...draft, required_actions: selection.requiredActions,
        optional_actions: selection.optionalActions, starting_actions: selection.startingActions })} />
    <details className="mission-advanced">
      <summary>{t($ => $.missions.builder.advanced)}</summary>
      <p className="form-hint">{t($ => $.missions.builder.advancedHint)}</p>
      {advanced.map(({ field, label }) => <div className="form-field" key={field}>
        <label htmlFor={`${identity}-${field}`}>{label}</label>
        <textarea id={`${identity}-${field}`} rows={5} value={draft[field]} disabled={disabled}
          spellCheck={false} autoCapitalize="none" onChange={event => onChange({ ...draft, [field]: event.target.value })} />
      </div>)}
    </details>
  </>;
}

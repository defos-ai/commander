import type { Mission } from '../api/types';
import { useTranslation } from '../i18n';

function ActionNames({ names }: { names: string[] | null | undefined }) {
  const { t } = useTranslation();
  return names?.length ? <ul className="mission-action-names">{names.map((name, index) => <li key={`${index}:${name}`}>{name}</li>)}</ul>
    : <p className="mission-muted">{t($ => $.missions.none)}</p>;
}

function object(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function MissionSchema({ schema, direction }: { schema: Mission['input_schema']; direction: 'input' | 'output' }) {
  const { t } = useTranslation();
  const properties = Object.entries(object(schema?.properties) ?? {});
  const required = Array.isArray(schema?.required) ? schema.required : [];
  return <section className="mission-section">
    <h2>{t($ => $.missions[direction])}</h2>
    {!schema ? <p className="mission-muted">{t($ => $.missions.noSchema)}</p> : <>
      {properties.length > 0 && <div className="mission-fields-scroll" tabIndex={0} role="region" aria-label={t($ => $.missions[direction])}>
        <table className="mission-fields">
          <thead><tr><th>{t($ => $.missions.field)}</th><th>{t($ => $.missions.type)}</th><th>{t($ => $.missions.descriptionLabel)}</th></tr></thead>
          <tbody>{properties.map(([name, value]) => {
            const field = object(value);
            const type = typeof field?.type === 'string' ? field.type
              : Array.isArray(field?.type) && field.type.every(item => typeof item === 'string') ? field.type.join(' | ')
                : t($ => $.missions.schemaType);
            return <tr key={name}>
              <th scope="row"><code>{name}</code><span className="mission-field-requirement">{required.includes(name) ? t($ => $.missions.required) : t($ => $.missions.optional)}</span></th>
              <td><code>{type}</code></td>
              <td>{typeof field?.description === 'string' && <p>{field.description}</p>}
                {field && 'default' in field && <p className="mission-muted">{t($ => $.missions.defaultValue, { value: JSON.stringify(field.default) })}</p>}
              </td>
            </tr>;
          })}</tbody>
        </table>
      </div>}
      <details className="mission-schema"><summary>{direction === 'input' ? t($ => $.missions.inputSchema) : t($ => $.missions.outputSchema)}</summary>
        <pre>{JSON.stringify(schema, null, 2)}</pre>
      </details>
    </>}
  </section>;
}

export function MissionDetails({ mission }: { mission: Mission }) {
  const { t } = useTranslation();
  const automations = Object.entries(mission.automations ?? {}).sort(([a], [b]) => a.localeCompare(b));
  return <div className="mission-details">
    <section className="mission-section"><h2>{t($ => $.missions.objective)}</h2><p className="mission-objective">{mission.objective}</p></section>
    <div className="mission-action-sections">
      <section className="mission-section"><h2>{t($ => $.missions.startingActions)}</h2><ActionNames names={mission.starting_actions} /></section>
      <section className="mission-section"><h2>{t($ => $.missions.requiredActions)}</h2><ActionNames names={mission.required_actions} /></section>
    </div>
    <section className="mission-section"><h2>{t($ => $.missions.automations)}</h2><p className="mission-muted">{t($ => $.missions.automationDescription)}</p>
      {automations.length ? <ul className="mission-automations">{automations.map(([name, rule]) => <li key={name}>
        <h3>{t($ => $.missions.sourceAction, { name })}</h3>
        {rule.run?.length ? <div><p className="mission-automation-label">{t($ => $.missions.run)}</p><ActionNames names={rule.run} />
          <p className="mission-muted">{rule.continue === 'when_all' ? t($ => $.missions.whenAll)
            : !rule.continue || rule.continue === 'when_each' ? t($ => $.missions.whenEach)
              : t($ => $.missions.continuation, { value: rule.continue })}</p>
        </div> : <p>{rule.decide === 'model' ? t($ => $.missions.modelReview) : t($ => $.missions.decision, { value: rule.decide ?? '' })}</p>}
      </li>)}</ul> : <p className="mission-empty-rules">{t($ => $.missions.noAutomations)}</p>}
    </section>
    {Boolean(mission.steps?.length) && <section className="mission-section"><h2>{t($ => $.missions.steps)}</h2><p className="mission-muted">{t($ => $.missions.stepsDescription)}</p>
      <ol className="mission-steps">{mission.steps?.map(step => <li key={step.id}>
        <h3>{step.id}</h3><p>{step.objective}</p>
        {Boolean(step.agent_types?.length) && <div><h4>{t($ => $.missions.agentTypes)}</h4><ActionNames names={step.agent_types} /></div>}
        {Boolean(step.required_actions?.length) && <div><h4>{t($ => $.missions.requiredActions)}</h4><ActionNames names={step.required_actions} /></div>}
      </li>)}</ol>
    </section>}
    <MissionSchema schema={mission.input_schema} direction="input" />
    <MissionSchema schema={mission.output_schema} direction="output" />
  </div>;
}

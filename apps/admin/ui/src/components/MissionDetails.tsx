import type { Mission } from '../api/types';
import { useTranslation } from '../i18n';
import { SchemaDetails } from './SchemaDetails';

function ActionNames({ names }: { names: string[] | null | undefined }) {
  const { t } = useTranslation();
  return names?.length ? <ul className="mission-action-names">{names.map((name, index) => <li key={`${index}:${name}`}>{name}</li>)}</ul>
    : <p className="mission-muted">{t($ => $.missions.none)}</p>;
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
    <SchemaDetails schema={mission.input_schema} direction="input" />
    <SchemaDetails schema={mission.output_schema} direction="output" />
  </div>;
}

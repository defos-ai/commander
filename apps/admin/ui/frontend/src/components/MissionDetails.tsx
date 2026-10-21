import type { Mission } from '../api/types';
import { useTranslation } from '../i18n';
import { SchemaDetails } from './SchemaDetails';
import { MissionRules } from './MissionRules';
import '../styles/catalog.css';

function ActionNames({ names }: { names: string[] | null | undefined }) {
  const { t } = useTranslation();
  return names?.length ? <ul className="mission-action-names">{names.map((name, index) => <li key={`${index}:${name}`}>{name}</li>)}</ul>
    : <p className="mission-muted">{t($ => $.missions.none)}</p>;
}

export function MissionDetails({ mission }: { mission: Mission }) {
  const { t } = useTranslation();
  return <div className="mission-details">
    <section className="catalog-section"><h2>{t($ => $.missions.objectives)}</h2>
      <ul className="catalog-tags">{mission.objectives.map((objective, index) => <li key={index}>{objective}</li>)}</ul>
    </section>
    <div className="mission-action-sections">
      <section className="mission-section"><h2>{t($ => $.missions.startingActions)}</h2><ActionNames names={mission.starting_actions} /></section>
      <section className="mission-section"><h2>{t($ => $.missions.requiredActions)}</h2><ActionNames names={mission.required_actions} /></section>
    </div>
    {Boolean(mission.optional_actions?.length) && <section className="mission-section"><h2>{t($ => $.missions.optionalActions)}</h2><ActionNames names={mission.optional_actions} /></section>}
    <section className="mission-section"><h2>{t($ => $.missions.rules)}</h2>
      <MissionRules rules={mission.rules} />
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

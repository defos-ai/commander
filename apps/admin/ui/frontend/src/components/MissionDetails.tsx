import type { Mission } from '../api/types';
import { useTranslation } from '../i18n';
import { SchemaDetails } from './SchemaDetails';
import { MissionContinuation } from './MissionContinuation';
import '../styles/catalog.css';

function ActionNames({ names }: { names: string[] | null | undefined }) {
  const { t } = useTranslation();
  return names?.length ? <ul className="mission-action-names">{names.map((name, index) => <li key={`${index}:${name}`}>{name}</li>)}</ul>
    : <p className="mission-muted">{t($ => $.missions.none)}</p>;
}

export function MissionDetails({ mission }: { mission: Mission }) {
  const { t } = useTranslation();
  const completionDecision = mission.completion?.decision_by ?? 'model';
  return <div className="mission-details">
    <section className="catalog-section"><h2>{t($ => $.missions.objectives)}</h2>
      <ul className="catalog-tags">{mission.objectives.map((objective, index) => <li key={index}>{objective}</li>)}</ul>
    </section>
    <section className="mission-section"><h2>{t($ => $.missions.completionDecision)}</h2>
      <p>{t($ => $.missions.completionDecisions[completionDecision])}</p>
      <p className="mission-muted">{t($ => $.missions.completionHints[completionDecision])}</p>
    </section>
    <div className="mission-action-sections">
      <section className="mission-section"><h2>{t($ => $.missions.startingActions)}</h2><ActionNames names={mission.starting_actions} /></section>
      <section className="mission-section"><h2>{t($ => $.missions.requiredActions)}</h2><ActionNames names={mission.required_actions} /></section>
    </div>
    {Boolean(mission.optional_actions?.length) && <section className="mission-section"><h2>{t($ => $.missions.optionalActions)}</h2><ActionNames names={mission.optional_actions} /></section>}
    {Boolean(mission.policies?.placement?.same_agent?.length) && <section className="mission-section"><h2>{t($ => $.missions.sameAgentActions)}</h2>
      <p className="mission-muted">{t($ => $.missionPolicies.sameAgentSetting)}</p>
      <ActionNames names={mission.policies?.placement?.same_agent} />
    </section>}
    <section className="mission-section"><h2>{t($ => $.missionPolicies.continuation)}</h2>
      <MissionContinuation policy={mission.policies?.continuation} />
    </section>
    <SchemaDetails schema={mission.input_schema} direction="input" />
    <SchemaDetails schema={mission.output_schema} direction="output" />
  </div>;
}

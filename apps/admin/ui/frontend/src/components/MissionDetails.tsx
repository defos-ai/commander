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
  const completionDecision = mission.policies?.completion?.decision_by ?? 'model';
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
    <section className="mission-section"><h2>{t($ => $.missionPolicies.continuation)}</h2>
      <MissionContinuation policy={mission.policies?.continuation} />
    </section>
    <section className="mission-section"><h2>{t($ => $.missionPolicies.sessionBindings)}</h2>
      <p className="mission-muted">{t($ => $.missionPolicies.sessionHint)}</p>
      {mission.policies?.session?.length ? <div className="mission-fields-scroll" tabIndex={0} role="region" aria-label={t($ => $.missionPolicies.sessionBindings)}>
        <table className="mission-fields" aria-label={t($ => $.missionPolicies.sessionBindings)}>
          <thead><tr>
            <th scope="col">{t($ => $.missionPolicies.createAction)}</th>
            <th scope="col">{t($ => $.missionPolicies.useActions)}</th>
          </tr></thead>
          <tbody>{mission.policies.session.map(policy => <tr key={policy.create_action}>
            <th scope="row">{policy.create_action}</th>
            <td><ActionNames names={policy.use_actions} /></td>
          </tr>)}</tbody>
        </table>
      </div> : <p className="mission-muted">{t($ => $.missionPolicies.noSessionBindings)}</p>}
    </section>
    <SchemaDetails schema={mission.input_schema} direction="input" />
    <SchemaDetails schema={mission.output_schema} direction="output" />
  </div>;
}

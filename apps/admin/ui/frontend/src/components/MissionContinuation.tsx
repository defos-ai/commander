import type { ContinuationPolicy } from '../api/types';
import { useTranslation } from '../i18n';
import '../styles/missions.css';

export function MissionContinuation({ policy }: { policy?: ContinuationPolicy | null }) {
  const { t } = useTranslation();
  const rows = [
    { name: t($ => $.missionPolicies.aggregateResults), actions: policy?.aggregate_results, effect: t($ => $.missionPolicies.allDirectChildren) },
    { name: t($ => $.missionPolicies.modelReview), actions: policy?.model_review, effect: t($ => $.missionPolicies.reviewProposals) },
  ].filter(row => row.actions?.length);

  return <>
    <p className="mission-muted">{t($ => $.missionPolicies.defaults)}</p>
    {rows.length ? <div className="mission-fields-scroll mission-continuation" tabIndex={0} role="region" aria-label={t($ => $.missionPolicies.continuation)}>
      <table className="mission-fields" aria-label={t($ => $.missionPolicies.continuation)}>
        <thead><tr>
          <th scope="col">{t($ => $.missionPolicies.behavior)}</th>
          <th scope="col">{t($ => $.missionPolicies.appliesTo)}</th>
          <th scope="col">{t($ => $.missionPolicies.effect)}</th>
        </tr></thead>
        <tbody>{rows.map(row => <tr key={row.name}>
          <th scope="row">{row.name}</th>
          <td><ul className="mission-action-names">{row.actions?.map(action => <li key={action}>{action}</li>)}</ul></td>
          <td>{row.effect}</td>
        </tr>)}</tbody>
      </table>
    </div> : <p className="mission-continuation-empty mission-muted">{t($ => $.missionPolicies.empty)}</p>}
  </>;
}

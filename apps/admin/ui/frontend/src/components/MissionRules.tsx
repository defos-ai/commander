import type { Mission, MissionRuleName } from '../api/types';
import { useTranslation } from '../i18n';
import '../styles/missions.css';

export function MissionRules({ rules }: { rules: Mission['rules'] }) {
  const { t } = useTranslation();
  const entries = Object.entries(rules ?? {});
  const labels: Record<MissionRuleName, { name: string; setting: string }> = {
    'aggregate-results': { name: t($ => $.missionRules.aggregateResults), setting: t($ => $.missionRules.allDirectChildren) },
    'decide-next-actions': { name: t($ => $.missionRules.decideNextActions), setting: t($ => $.missionRules.model) },
    'same-agent': { name: t($ => $.missionRules.sameAgent), setting: t($ => $.missionRules.sameAgentSetting) },
  };

  return <>
    <p className="mission-muted">{t($ => $.missionRules.defaults)}</p>
    {entries.length ? <div className="mission-fields-scroll mission-rules" tabIndex={0} role="region" aria-label={t($ => $.missionRules.title)}>
      <table className="mission-fields" aria-label={t($ => $.missionRules.title)}>
        <thead><tr>
          <th scope="col">{t($ => $.missionRules.rule)}</th>
          <th scope="col">{t($ => $.missionRules.appliesTo)}</th>
          <th scope="col">{t($ => $.missionRules.settings)}</th>
        </tr></thead>
        <tbody>{entries.map(([name, rule]) => <tr key={name}>
          <th scope="row">{labels[name as MissionRuleName].name}</th>
          <td><ul className="mission-action-names">{rule.for_actions.map(action => <li key={action}>{action}</li>)}</ul></td>
          <td>{labels[name as MissionRuleName].setting}</td>
        </tr>)}</tbody>
      </table>
    </div> : <p className="mission-empty-rules mission-muted">{t($ => $.missionRules.empty)}</p>}
  </>;
}

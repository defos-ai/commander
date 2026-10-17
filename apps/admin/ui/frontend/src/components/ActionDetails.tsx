import type { Action } from '../api/types';
import { useTranslation } from '../i18n';
import { SchemaDetails } from './SchemaDetails';

export function ActionStatus({ status }: { status: string }) {
  const { t } = useTranslation();
  const known = status === 'ready' || status === 'unavailable' || status === 'unknown';
  return <span className="catalog-status" data-tone={status === 'ready' ? 'ready' : status === 'unavailable' ? 'warning' : 'muted'}>
    {known ? t($ => $.actions.statuses[status]) : status}
  </span>;
}

export function ActionDetails({ action }: { action: Action }) {
  const { t } = useTranslation();
  const hasReadiness = action.status !== '' && action.status !== 'unknown';
  return <div className="catalog-details">
    <section className="catalog-section"><h2>{t($ => $.actions.descriptionLabel)}</h2><p>{action.description}</p></section>
    <section className="catalog-section"><h2>{t($ => $.actions.readiness)}</h2>
      {hasReadiness && <ActionStatus status={action.status} />}
      {hasReadiness && action.status_reason && <p>{action.status_reason}</p>}
      <p className="catalog-muted">{t($ => $.actions.readinessNote)}</p>
    </section>
    {hasReadiness && <section className="catalog-section"><h2>{t($ => $.actions.requirements)}</h2>
      {action.missing_requirements?.length ? <dl className="detail-list">{action.missing_requirements.map((requirement, index) => {
        const kind = requirement.kind;
        return <div key={`${kind}:${requirement.name}:${index}`}>
          <dt>{kind === 'executable' || kind === 'configuration' || kind === 'secret' ? t($ => $.actions.requirementKinds[kind]) : kind}</dt>
          <dd><code>{requirement.name}</code>{kind === 'configuration' && requirement.secret && <span className="catalog-muted"> · {t($ => $.actions.secret)}</span>}</dd>
        </div>;
      })}</dl> : <p className="catalog-muted">{t($ => $.actions.noRequirements)}</p>}
    </section>}
    <SchemaDetails schema={action.input_schema} direction="input" />
    <SchemaDetails schema={action.output_schema} direction="output" />
  </div>;
}

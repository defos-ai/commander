import { useId, useState } from 'react';
import { LoaderCircle, RefreshCw, Search } from 'lucide-react';
import { getAction, listActions } from '../api/actions';
import { getAgent, listAgents } from '../api/agents';
import type { Action, Agent } from '../api/types';
import type { Connection, Session } from '../connections/types';
import { useCatalogQuery } from '../hooks/useCatalogQuery';
import { useTranslation } from '../i18n';
import type { ActionDefinitionDraft } from '../missions/action-definitions';
import { ActionDefinitionDialog } from './ActionDefinitionDialog';
import { ActionDetails, ActionStatus } from './ActionDetails';
import { AgentStatus } from './AgentDetails';
import { Button } from './Button';
import '../styles/catalog.css';

export type MissionActionSelection = {
  requiredActions: string[];
  optionalActions: string[];
  startingActions: string[];
};

type Props = MissionActionSelection & {
  connection: Connection;
  credentials?: Session;
  disabled?: boolean;
  definitions?: ActionDefinitionDraft[];
  invalidDefinitions?: boolean;
  onSaveDefinition?: (definition: ActionDefinitionDraft, originalName?: string) => void;
  onRemoveDefinition?: (name: string) => void;
  onChange: (selection: MissionActionSelection) => void;
  onSignIn: () => void;
};

export function MissionActionPicker({ connection, credentials, requiredActions, optionalActions,
  startingActions, disabled, definitions = [], invalidDefinitions, onSaveDefinition, onRemoveDefinition, onChange, onSignIn }: Props) {
  const { t } = useTranslation();
  const identity = useId();
  const [search, setSearch] = useState('');
  const [definitionDialog, setDefinitionDialog] = useState<{ definition?: ActionDefinitionDraft }>();
  const query = useCatalogQuery<Action>({ url: connection.url, credentials, list: listActions, get: getAction });
  const agentQuery = useCatalogQuery<Agent>({ url: connection.url, credentials, list: listAgents, get: getAgent });
  const actions = new Map((query.items ?? []).map(action => [action.name, action]));
  const desired = new Map(definitions.map(definition => [definition.name, definition]));
  // Retain the draft when agents disappear or the catalog cannot be loaded.
  const names = [...new Set([...requiredActions, ...optionalActions, ...startingActions, ...actions.keys(), ...desired.keys()])];
  const filter = search.trim().toLocaleLowerCase();
  const visible = names.filter(name => `${name} ${desired.get(name)?.description ?? actions.get(name)?.description ?? ''}`.toLocaleLowerCase().includes(filter))
    .sort((left, right) => left.localeCompare(right));
  const error = !credentials ? 'authentication' : query.error?.kind;
  const catalogLoaded = query.items !== undefined;
  const refreshing = query.refreshing || agentQuery.refreshing;

  function refresh() { query.refresh(); agentQuery.refresh(); }

  function setRole(name: string, role: string) {
    if (disabled) return;
    onChange({
      requiredActions: role === 'required' ? [...new Set([...requiredActions, name])] : requiredActions.filter(value => value !== name),
      optionalActions: role === 'optional' ? [...new Set([...optionalActions, name])] : optionalActions.filter(value => value !== name),
      startingActions: role === 'required' ? startingActions : startingActions.filter(value => value !== name),
    });
  }

  function setStarting(name: string, checked: boolean) {
    if (disabled || !requiredActions.includes(name)) return;
    onChange({ requiredActions, optionalActions,
      startingActions: checked ? [...new Set([...startingActions, name])] : startingActions.filter(value => value !== name) });
  }

  return <section className="mission-action-picker" aria-labelledby={`${identity}-title`}>
    <h2 id={`${identity}-title`}>{t($ => $.missions.builder.actions)}</h2>
    <p className="form-hint">{t($ => $.missions.builder.actionsHint)}</p>
    <div className="mission-action-toolbar">
      <label className="catalog-search"><Search size={17} aria-hidden="true" />
        <input type="search" aria-label={t($ => $.missions.builder.searchActions)}
          placeholder={t($ => $.missions.builder.searchActionsPlaceholder)} value={search}
          onChange={event => setSearch(event.target.value)} />
      </label>
      <div className="mission-action-toolbar-buttons">{credentials && <Button variant="secondary" onClick={refresh} disabled={refreshing}>
        <RefreshCw size={15} aria-hidden="true" />
        {refreshing ? t($ => $.missions.builder.refreshingActions) : t($ => $.missions.builder.refreshActions)}
      </Button>}
      {onSaveDefinition && <Button variant="secondary" disabled={disabled || invalidDefinitions} onClick={() => setDefinitionDialog({})}>
        {t($ => $.missions.actionDefinition.createTitle)}
      </Button>}</div>
    </div>
    {invalidDefinitions && <p className="form-error" role="alert">{t($ => $.missions.actionDefinition.invalidDefinitions)}</p>}
    {error && <div className="notice" role="alert">
      <p>{error === 'authentication' ? t($ => $.missions.builder.actionsSignIn)
        : error === 'forbidden' ? t($ => $.missions.builder.actionsForbidden)
          : error === 'invalid_response' ? t($ => $.missions.builder.actionsInvalidResponse)
            : t($ => $.missions.builder.actionsUnavailable)}</p>
      {error === 'authentication'
        ? <Button onClick={onSignIn}>{t($ => $.missions.builder.signIn)}</Button>
        : <Button variant="secondary" onClick={refresh}>{t($ => $.missions.builder.retryActions)}</Button>}
    </div>}
    {query.loading && <p className="catalog-loading" role="status">
      <LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />{t($ => $.missions.builder.loadingActions)}
    </p>}
    {visible.length > 0 ? <ul className="mission-action-list">
      {visible.map(name => {
        const action = actions.get(name);
        const definition = desired.get(name);
        const contract: Action | undefined = definition ? { ...definition, status: 'unknown',
          input_schema: JSON.parse(definition.input_schema), output_schema: JSON.parse(definition.output_schema) } : action;
        const role = requiredActions.includes(name) ? 'required' : optionalActions.includes(name) ? 'optional' : 'none';
        const reports = (agentQuery.items ?? []).flatMap(agent => {
          const reportedAction = agent.actions?.find(candidate => candidate.name === name);
          return reportedAction ? [{ agent, action: reportedAction }] : [];
        });
        return <li className="mission-action-item" key={name}>
          <div className="mission-action-summary"><strong>{name}</strong>
            {definition && <span className="catalog-status" data-tone="muted">{t($ => $.missions.actionDefinition.defined)}</span>}
            {action ? action.status !== '' && action.status !== 'unknown' && <ActionStatus status={action.status} />
              : catalogLoaded && <span className="catalog-status" data-tone="muted">{t($ => $.missions.builder.notInstalled)}</span>}
          </div>
          <p className="mission-action-description">{definition?.description ?? action?.description ?? (catalogLoaded
            ? t($ => $.missions.builder.retainedAction) : t($ => $.missions.builder.unavailableCatalogAction))}</p>
          {action?.status_reason && <p className="form-hint">{action.status_reason}</p>}
          <div className="mission-action-controls">
            <select className="mission-action-role" aria-label={t($ => $.missions.builder.actionRole, { name })}
              value={role} disabled={disabled} onChange={event => setRole(name, event.target.value)}>
              <option value="none" disabled={Boolean(definition)}>{t($ => $.missions.builder.roleNone)}</option>
              <option value="required">{t($ => $.missions.builder.roleRequired)}</option>
              <option value="optional">{t($ => $.missions.builder.roleOptional)}</option>
            </select>
            <label className="mission-action-starting"><input type="checkbox" checked={startingActions.includes(name)}
              aria-label={t($ => $.missions.builder.startingAction, { name })} disabled={disabled || role !== 'required'}
              onChange={event => setStarting(name, event.target.checked)} />{t($ => $.missions.builder.starting)}</label>
          </div>
          {definition && <div className="mission-action-definition-controls">
            {onSaveDefinition && <Button variant="secondary" disabled={disabled} onClick={() => setDefinitionDialog({ definition })}>
              {t($ => $.missions.actionDefinition.edit, { name })}
            </Button>}
            {onRemoveDefinition && <Button variant="secondary" disabled={disabled} title={t($ => $.missions.actionDefinition.removeHint)}
              onClick={() => onRemoveDefinition(name)}>{t($ => $.missions.actionDefinition.remove, { name })}</Button>}
            <p className="form-hint">{t($ => $.missions.actionDefinition.deselectHint)}</p>
          </div>}
          {contract && <details className="mission-action-inspector">
            <summary>{t($ => $.missions.builder.inspectAction, { name })}</summary>
            <section className="mission-action-agents"><h3>{t($ => $.missions.builder.agentReports)}</h3>
              {agentQuery.error ? <p className="catalog-muted">{t($ => $.missions.builder.agentReportsUnavailable)}</p>
                : agentQuery.loading ? <p className="catalog-muted" role="status">{t($ => $.missions.builder.agentReportsLoading)}</p>
                  : reports.length ? <ul className="mission-agent-reports">{reports.map(report => <li key={report.agent.id}>
                    <strong>{report.agent.id}</strong> <span className="catalog-muted">{report.agent.type}</span>
                    <dl className="detail-list">
                      <div><dt>{t($ => $.missions.builder.agentStatus)}</dt><dd><AgentStatus status={report.agent.status} /></dd></div>
                      <div><dt>{t($ => $.missions.builder.reportedActionReadiness)}</dt><dd><ActionStatus status={report.action.status} /></dd></div>
                    </dl>
                    {report.agent.stale && <p className="form-hint">{t($ => $.missions.builder.staleAgentReport)}</p>}
                    {report.action.status_reason && <p>{report.action.status_reason}</p>}
                    {Boolean(report.action.missing_requirements?.length) && <ul className="catalog-tags">
                      {report.action.missing_requirements?.map((requirement, index) => <li key={`${requirement.kind}:${requirement.name}:${index}`}>
                        <code>{requirement.name}</code>
                      </li>)}
                    </ul>}
                  </li>)}</ul> : <p className="catalog-muted">{t($ => $.missions.builder.noAgentReports)}</p>}
            </section>
            <ActionDetails action={contract} />
          </details>}
        </li>;
      })}
    </ul> : !query.loading && !error && <p className="catalog-muted">{filter
      ? t($ => $.missions.builder.noMatchingActions) : t($ => $.missions.builder.noActions)}</p>}
    {definitionDialog && !disabled && onSaveDefinition && <ActionDefinitionDialog definition={definitionDialog.definition}
      onClose={() => setDefinitionDialog(undefined)} onSave={definition => {
        onSaveDefinition(definition, definitionDialog.definition?.name);
        setSearch(definition.name);
        setDefinitionDialog(undefined);
      }} />}
  </section>;
}

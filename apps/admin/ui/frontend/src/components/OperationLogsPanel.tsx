import { useEffect, useId, useLayoutEffect, useRef, useState, type FormEvent } from 'react';
import { ArrowDownToLine, History, LoaderCircle, Pause, Play } from 'lucide-react';
import type { OperationLogFilters } from '../api/operation-logs';
import type { OperationTask } from '../api/operation-types';
import type { Session } from '../connections/types';
import { useOperationLogs } from '../hooks/useOperationLogs';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import '../styles/operation-logs.css';

const emptyFilters: OperationLogFilters = {};
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const agentID = /^[a-z0-9]+(?:-[a-z0-9]+)*-agent-[0-9a-f]{8}$/;

export function OperationLogsPanel({ url, credentials, operationID, tasks, filters: routeFilters, onFiltersChange, onSignIn }: {
  url: string;
  credentials?: Session;
  operationID: string;
  tasks: OperationTask[];
  filters?: OperationLogFilters;
  onFiltersChange?: (filters: OperationLogFilters) => void;
  onSignIn: () => void;
}) {
  const { t } = useTranslation();
  const identity = useId();
  const [localFilters, setLocalFilters] = useState(routeFilters ?? emptyFilters);
  const filters = onFiltersChange ? routeFilters ?? emptyFilters : localFilters;
  const [taskInput, setTaskInput] = useState(filters.task_id ?? '');
  const [agentInput, setAgentInput] = useState(filters.agent_id ?? '');
  const [actionInput, setActionInput] = useState(filters.action_name ?? '');
  const [invalid, setInvalid] = useState(false);
  const area = useRef<HTMLDivElement>(null);
  const olderAnchor = useRef<{ id: string; offset: number; top: number } | undefined>(undefined);
  const logs = useOperationLogs(url, credentials, operationID, filters);
  const error = !credentials ? 'authentication' : logs.error?.kind;
  const taskOptions = [...new Map(tasks.map(task => [task.id, task])).values()];

  useEffect(() => { setLocalFilters(routeFilters ?? emptyFilters); }, [routeFilters?.task_id, routeFilters?.agent_id, routeFilters?.action_name]);
  useEffect(() => {
    setTaskInput(filters.task_id ?? ''); setAgentInput(filters.agent_id ?? ''); setActionInput(filters.action_name ?? ''); setInvalid(false);
    olderAnchor.current = undefined;
  }, [url, credentials, operationID, filters.task_id, filters.agent_id, filters.action_name]);

  useLayoutEffect(() => {
    const element = area.current;
    if (!element) return;
    if (logs.following) {
      element.scrollTop = element.scrollHeight;
      olderAnchor.current = undefined;
    } else if (olderAnchor.current && logs.entries[0]?.id !== olderAnchor.current.id) {
      const anchor = olderAnchor.current;
      const row = element.querySelector<HTMLElement>('[data-log-id="' + anchor.id + '"]');
      if (row) element.scrollTop = anchor.top + row.offsetTop - anchor.offset;
      olderAnchor.current = undefined;
    }
  }, [logs.entries, logs.following]);

  function setFilters(next: OperationLogFilters) {
    if (onFiltersChange) onFiltersChange(next); else setLocalFilters(next);
  }
  function applyFilters(event: FormEvent) {
    event.preventDefault();
    const agent = agentInput.trim();
    if (taskInput && !uuid.test(taskInput) || agent && (!agentID.test(agent) || agent.length > 63) ||
      actionInput !== '' && actionInput.trim() === '') { setInvalid(true); return; }
    setInvalid(false);
    setFilters({ ...(taskInput ? { task_id: taskInput } : {}), ...(agent ? { agent_id: agent } : {}),
      ...(actionInput ? { action_name: actionInput } : {}) });
  }
  function clearFilters() {
    setTaskInput(''); setAgentInput(''); setActionInput(''); setInvalid(false); setFilters({});
  }
  function loadOlder() {
    const element = area.current;
    const id = logs.entries[0]?.id;
    const row = id && element?.querySelector<HTMLElement>('[data-log-id="' + id + '"]');
    if (element && id && row) olderAnchor.current = { id, offset: row.offsetTop, top: element.scrollTop };
    logs.loadOlder();
  }

  return <section className="operation-logs catalog-section" aria-label={t($ => $.operationLogs.title)}>
    <div className="operation-log-heading">
      <div><h2>{t($ => $.operationLogs.title)}</h2><p className="catalog-muted">{t($ => $.operationLogs.description)}</p></div>
      <div className="operation-log-controls">
        <Button variant="secondary" disabled={!credentials} onClick={logs.following ? logs.pause : logs.resume}>
          {logs.following ? <Pause size={15} aria-hidden="true" /> : <Play size={15} aria-hidden="true" />}
          {logs.following ? t($ => $.operationLogs.pause) : t($ => $.operationLogs.follow)}
        </Button>
        <Button variant="secondary" disabled={!credentials} onClick={logs.jumpToLatest}>
          <ArrowDownToLine size={15} aria-hidden="true" />{t($ => $.operationLogs.latest)}
        </Button>
      </div>
    </div>
    <form className="operation-log-filters" aria-label={t($ => $.operationLogs.filters)} onSubmit={applyFilters}>
      <label htmlFor={identity + '-task'}>{t($ => $.operationLogs.task)}
        <select id={identity + '-task'} value={taskInput} onChange={event => setTaskInput(event.target.value)}>
          <option value="">{t($ => $.operationLogs.allTasks)}</option>
          {taskInput && !taskOptions.some(task => task.id === taskInput) && <option value={taskInput}>{taskInput}</option>}
          {taskOptions.map(task => <option key={task.id} value={task.id}>{task.action_name} · {task.id}</option>)}
        </select>
      </label>
      <label htmlFor={identity + '-agent'}>{t($ => $.operationLogs.agent)}
        <input id={identity + '-agent'} value={agentInput} onChange={event => setAgentInput(event.target.value)} spellCheck={false} />
      </label>
      <label htmlFor={identity + '-action'}>{t($ => $.operationLogs.action)}
        <input id={identity + '-action'} value={actionInput} onChange={event => setActionInput(event.target.value)} spellCheck={false} />
      </label>
      <div className="operation-log-filter-actions">
        <Button type="submit" variant="secondary">{t($ => $.operationLogs.apply)}</Button>
        <Button variant="secondary" onClick={clearFilters}>{t($ => $.operationLogs.clear)}</Button>
      </div>
      {invalid && <p className="notice" role="alert">{t($ => $.operationLogs.invalidFilters)}</p>}
    </form>
    {error && <div className="notice operation-log-error" role="alert">
      <p>{t($ => $.operationLogs.errors[error])}</p>
      {error === 'authentication' ? <Button variant="secondary" onClick={onSignIn}>{t($ => $.operationLogs.signIn)}</Button>
        : <Button variant="secondary" disabled={logs.fetching} onClick={logs.retry}>{t($ => $.operationLogs.retry)}</Button>}
    </div>}
    <div className="operation-log-toolbar">
      <Button variant="secondary" onClick={loadOlder} disabled={!credentials || !logs.hasOlder || logs.fetching}>
        {logs.loadingOlder ? <LoaderCircle className="connection-form-spinner" size={15} aria-hidden="true" /> : <History size={15} aria-hidden="true" />}
        {t($ => $.operationLogs.older)}
      </Button>
      <p role="status">{logs.following ? t($ => $.operationLogs.following) : t($ => $.operationLogs.paused)}
        {' · '}{t($ => $.operationLogs.count, { count: logs.entries.length })}</p>
    </div>
    {logs.truncated && <p className="form-hint">{t($ => $.operationLogs.truncated)}</p>}
    {logs.loading && <p className="catalog-loading" role="status">
      <LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />{t($ => $.operationLogs.loading)}
    </p>}
    <div className="operation-log-entries" role="log" aria-label={t($ => $.operationLogs.entries)} aria-live="off" tabIndex={0} ref={area}
      onScroll={event => {
        const element = event.currentTarget;
        if (logs.following && element.scrollHeight > element.clientHeight && element.scrollTop + element.clientHeight < element.scrollHeight - 24) logs.pause();
      }}>
      {logs.entries.length ? <ol>{logs.entries.map(entry => <li key={entry.id} data-log-id={entry.id}>
        <div className="operation-log-meta">
          <time dateTime={entry.created_at}>{new Date(entry.created_at).toLocaleString()}</time>
          <code>{entry.agent_id}</code><code>{entry.action_name}</code>
        </div>
        <pre>{entry.message}</pre>
        <p className="operation-log-task">{t($ => $.operationLogs.task)}: <code>{entry.task_id}</code></p>
      </li>)}</ol> : !logs.loading && <p className="catalog-muted operation-log-empty">{t($ => $.operationLogs.empty)}</p>}
    </div>
  </section>;
}

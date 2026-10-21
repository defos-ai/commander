import type {
  Operation, OperationAffinity, OperationArtifact, OperationContinuation, OperationNextAction,
  OperationPreflight, OperationQuestion, OperationTask,
} from '../api/operation-types';
import type { ActionRequirement } from '../api/types';
import { useTranslation } from '../i18n';
import { useQuestionExpiry } from '../hooks/useQuestionExpiry';
import { Button } from './Button';
import '../styles/catalog.css';
import '../styles/operations.css';

export function isActiveOperation(status: string) {
  return status === 'planning' || status === 'running' || status === 'waiting';
}

export function OperationSecretsNotice({ operation, onManageSecrets }: { operation: Operation; onManageSecrets?: () => void }) {
  const { t } = useTranslation();
  if (!isActiveOperation(operation.status)) return null;
  const scopes = new Set<string>();
  const addRequirements = (type: string | undefined, requirements?: ActionRequirement[] | null) => {
    if (!type) return;
    for (const requirement of requirements ?? []) {
      if (requirement.kind === 'secret') scopes.add(`${type} / ${requirement.name}`);
    }
  };
  for (const action of operation.preflight?.ready ? [] : operation.preflight?.actions ?? []) {
    if (action.ready_agent_ids?.length) continue;
    for (const agent of action.unavailable_agents ?? []) {
      addRequirements(agent.agent_type, agent.missing_requirements);
    }
  }
  for (const task of operation.tasks ?? []) {
    if (task.status !== 'running' || !task.deadline_at || Date.parse(task.deadline_at) <= Date.now()) continue;
    addRequirements(task.agent_type, task.missing_requirements);
  }
  if (!scopes.size) return null;
  return <div className="notice operation-secrets" role="status">
    <strong>{t($ => $.operations.secretsNeeded)}</strong>
    <p>{t($ => $.operations.secretsNeededDescription)}</p>
    <ul className="catalog-tags">{[...scopes].sort().map(scope => <li key={scope}><code>{scope}</code></li>)}</ul>
    {onManageSecrets && <Button variant="secondary" onClick={onManageSecrets}>{t($ => $.operations.manageSecrets)}</Button>}
  </div>;
}

export function OperationStatus({ status }: { status: string }) {
  const { t } = useTranslation();
  const label = status === 'planning' || status === 'running' || status === 'waiting' || status === 'completed' || status === 'failed' || status === 'cancelled'
    ? t($ => $.operations.statuses[status]) : status;
  return <span className="catalog-status" data-tone={status === 'completed' ? 'ready' : status === 'failed' ? 'warning' : 'muted'}>{label}</span>;
}

export function TaskStatus({ status }: { status: string }) {
  const { t } = useTranslation();
  const label = status === 'queued' || status === 'running' || status === 'succeeded' || status === 'failed' || status === 'cancelled' || status === 'interrupted'
    ? t($ => $.operations.taskStatuses[status]) : status;
  return <span className="catalog-status" data-tone={status === 'succeeded' ? 'ready' : status === 'failed' || status === 'interrupted' ? 'warning' : 'muted'}>{label}</span>;
}

function Timestamp({ value }: { value?: string | null }) {
  const { t } = useTranslation();
  const date = value ? new Date(value) : null;
  return date && !Number.isNaN(date.getTime()) && !value?.startsWith('0001-')
    ? <time dateTime={value ?? undefined} title={value ?? undefined}>{date.toLocaleString()}</time>
    : <span className="catalog-muted">{t($ => $.operations.notReported)}</span>;
}

export function OperationData({ label, value }: { label: string; value: unknown }) {
  if (value === undefined) return null;
  return <details className="operation-data"><summary>{label}</summary><pre>{JSON.stringify(value, null, 2)}</pre></details>;
}

function ProposedActions({ actions }: { actions: OperationNextAction[] | null | undefined }) {
  const { t } = useTranslation();
  return <div className="operation-nested"><h4>{t($ => $.operations.proposedActions)}</h4>
    {actions?.length ? <><p className="catalog-muted">{t($ => $.operations.proposedNote)}</p><ul className="operation-proposals">{actions.map((action, index) => <li key={index}>
      <code>{action.action_name}</code>{action.reason && <p>{action.reason}</p>}
      <OperationData label={t($ => $.operations.input)} value={action.input} />
    </li>)}</ul></> : <p className="catalog-muted">{t($ => $.operations.noProposedActions)}</p>}
  </div>;
}

function Continuation({ continuation }: { continuation: OperationContinuation }) {
  const { t } = useTranslation();
  const { status, decision_by: decision } = continuation;
  return <div className="operation-nested"><h3>{t($ => $.operations.continuation)}</h3>
    <dl className="detail-list">
      <div><dt>{t($ => $.operations.status)}</dt><dd>{status === 'pending' || status === 'waiting' || status === 'resolved' ? t($ => $.operations.continuationStatuses[status]) : status}</dd></div>
      <div><dt>{t($ => $.operations.decisionBy)}</dt><dd>{decision === 'code' || decision === 'model' || decision === 'user' ? t($ => $.operations.decisions[decision]) : decision}</dd></div>
      <div><dt>{t($ => $.operations.version)}</dt><dd>{continuation.version}</dd></div>
      {continuation.question_id && <div><dt>{t($ => $.operations.questionId)}</dt><dd><code>{continuation.question_id}</code></dd></div>}
      {Boolean(continuation.created_task_ids?.length) && <div><dt>{t($ => $.operations.createdTasks)}</dt><dd><ul className="operation-identifiers">{continuation.created_task_ids?.map(id => <li key={id}><code>{id}</code></li>)}</ul></dd></div>}
    </dl>
    <ProposedActions actions={continuation.proposed_actions} />
  </div>;
}

function Task({ task, queuedFor }: { task: OperationTask; queuedFor?: string }) {
  const { t } = useTranslation();
  return <details className="operation-card"><summary>
    <span className="operation-card-name"><span>{task.action_name}</span><code>{task.id}</code>
      {queuedFor && <span className="catalog-muted">{t($ => $.operations.queuedForAgent, { agent: queuedFor })}</span>}
    </span><TaskStatus status={task.status} />
  </summary><div className="operation-card-body">
    {task.status === 'interrupted' && <p className="notice">{t($ => $.operations.interruptedNote)}</p>}
    {task.error && <div className="operation-error"><h3>{t($ => $.operations.error)}</h3><p>{task.error}</p></div>}
    <dl className="detail-list">
      <div><dt>{t($ => $.operations.agentId)}</dt><dd>{task.agent_id ? <code>{task.agent_id}</code> : t($ => $.operations.unassigned)}</dd></div>
      {task.parent_task_id && <div><dt>{t($ => $.operations.parentTaskId)}</dt><dd><code>{task.parent_task_id}</code></dd></div>}
      {task.step_id && <div><dt>{t($ => $.operations.stepId)}</dt><dd><code>{task.step_id}</code></dd></div>}
      {task.command_id && <div><dt>{t($ => $.operations.commandId)}</dt><dd><code>{task.command_id}</code></dd></div>}
      <div><dt>{t($ => $.operations.startedAt)}</dt><dd><Timestamp value={task.started_at} /></dd></div>
      {task.finished_at && <div><dt>{t($ => $.operations.finishedAt)}</dt><dd><Timestamp value={task.finished_at} /></dd></div>}
      {task.deadline_at && <div><dt>{t($ => $.operations.deadlineAt)}</dt><dd><Timestamp value={task.deadline_at} /></dd></div>}
    </dl>
    <OperationData label={t($ => $.operations.input)} value={task.input} />
    {task.result ? <>
      <OperationData label={t($ => $.operations.result)} value={task.result.output} />
      {Boolean(task.result.artifacts?.length) && <div className="operation-nested"><h3>{t($ => $.operations.artifacts)}</h3>
        <ul className="operation-identifiers">{task.result.artifacts?.map(artifact => <li key={artifact.id}>{artifact.name} · <code>{artifact.id}</code></li>)}</ul>
      </div>}
      {!task.continuation && Boolean(task.result.next_actions?.length) && <ProposedActions actions={task.result.next_actions} />}
    </> : <p className="catalog-muted">{t($ => $.operations.noResult)}</p>}
    {task.continuation && <Continuation continuation={task.continuation} />}
  </div></details>;
}

function Question({ question, onAnswer }: { question: OperationQuestion; onAnswer?: () => void }) {
  const { t } = useTranslation();
  const expired = useQuestionExpiry(question.expires_at);
  const { status } = question;
  return <article className="operation-question">
    <div className="operation-question-heading"><h3>{question.text}</h3><span className="catalog-status" data-tone={status === 'answered' ? 'ready' : 'muted'}>
      {status === 'pending' || status === 'answered' || status === 'expired' ? t($ => $.operations.questionStatuses[status]) : status}
    </span></div>
    {Boolean(question.options?.length) && <div className="operation-nested"><h4>{t($ => $.operations.options)}</h4><ol>{question.options?.map((option, index) => <li key={index}>{option}</li>)}</ol></div>}
    {question.answer && <div className="operation-nested"><h4>{t($ => $.operations.answer)}</h4><p>{question.answer}</p></div>}
    {status === 'pending' && (expired
      ? <p className="operation-nested catalog-muted">{t($ => $.operationCommands.expired)}</p>
      : onAnswer && <Button className="operation-nested" onClick={onAnswer}>{t($ => $.operationCommands.answerTitle)}</Button>)}
    <dl className="detail-list">
      <div><dt>{t($ => $.operations.questionId)}</dt><dd><code>{question.id}</code></dd></div>
      {question.task_id && <div><dt>{t($ => $.operations.taskId)}</dt><dd><code>{question.task_id}</code></dd></div>}
      {question.step_id && <div><dt>{t($ => $.operations.stepId)}</dt><dd><code>{question.step_id}</code></dd></div>}
      <div><dt>{t($ => $.operations.askedAt)}</dt><dd><Timestamp value={question.asked_at} /></dd></div>
      <div><dt>{t($ => $.operations.expiresAt)}</dt><dd><Timestamp value={question.expires_at} /></dd></div>
      {question.answered_at && <div><dt>{t($ => $.operations.answeredAt)}</dt><dd><Timestamp value={question.answered_at} /></dd></div>}
    </dl>
  </article>;
}

function Affinity({ affinity }: { affinity: OperationAffinity }) {
  const { t } = useTranslation();
  return <section className="catalog-section"><h2>{t($ => $.missionRules.sameAgent)}</h2>
    {affinity.error ? <p className="notice" role="alert">{affinity.error}</p> : <>
      <p className="catalog-muted">{t($ => $.operations.affinityDescription)}</p>
      {affinity.agent_id ? <dl className="detail-list">
        <div><dt>{t($ => $.operations.agentId)}</dt><dd><code>{affinity.agent_id}</code></dd></div>
      </dl> : <p className="catalog-muted">{t($ => $.operations.affinityUnbound)}</p>}
    </>}
    <ul className="catalog-tags">{affinity.for_actions.map(action => <li key={action}><code>{action}</code></li>)}</ul>
  </section>;
}

function Preflight({ preflight }: { preflight: OperationPreflight }) {
  const { t } = useTranslation();
  return <section className="catalog-section"><h2>{t($ => $.operations.preflight)}</h2>
    <p className={preflight.ready ? 'catalog-muted' : 'notice'}>{preflight.ready ? t($ => $.operations.preflightReady) : t($ => $.operations.preflightBlocked)}</p>
    <ul className="operation-items">{preflight.actions?.map(action => <li key={action.action_name}>
      <details className="operation-card"><summary><span className="operation-card-name">{action.action_name}</span>
        <span className="catalog-status" data-tone={action.ready_agent_ids?.length ? 'ready' : 'warning'}>{action.ready_agent_ids?.length
          ? t($ => $.operations.readyAgents, { count: action.ready_agent_ids.length }) : t($ => $.operations.noReadyAgents)}</span>
      </summary><div className="operation-card-body">
        {Boolean(action.ready_agent_ids?.length) && <ul className="operation-identifiers">{action.ready_agent_ids?.map(id => <li key={id}><code>{id}</code></li>)}</ul>}
        {action.unavailable_agents?.map(agent => <div key={agent.agent_id} className="operation-nested"><code>{agent.agent_id}</code><p>{agent.reason}</p>
          {Boolean(agent.missing_requirements?.length) && <div className="operation-nested"><h4>{t($ => $.operations.missingRequirements)}</h4>
            <ul className="catalog-tags">{agent.missing_requirements?.map((requirement, index) => {
              const { kind } = requirement;
              return <li key={index}>{kind === 'executable' || kind === 'configuration' || kind === 'secret' ? t($ => $.agents.requirementKinds[kind]) : kind}: <code>{requirement.name}</code>{kind === 'configuration' && requirement.sensitive && <> · {t($ => $.agents.sensitive)}</>}</li>;
            })}</ul>
          </div>}
        </div>)}
      </div></details>
    </li>)}</ul>
  </section>;
}

function Artifact({ artifact, onOpen }: { artifact: OperationArtifact; onOpen?: (fileID?: string) => void }) {
  const { t } = useTranslation();
  return <details className="operation-card"><summary><span className="operation-card-name"><span>{artifact.name}</span><code>{artifact.id}</code></span></summary>
    <div className="operation-card-body"><dl className="detail-list">
      <div><dt>{t($ => $.operations.action)}</dt><dd>{artifact.action_name}</dd></div>
      <div><dt>{t($ => $.operations.agentId)}</dt><dd><code>{artifact.agent_id}</code></dd></div>
      {artifact.command_id && <div><dt>{t($ => $.operations.commandId)}</dt><dd><code>{artifact.command_id}</code></dd></div>}
      <div><dt>{t($ => $.operations.createdAt)}</dt><dd><Timestamp value={artifact.created_at} /></dd></div>
    </dl>
      {onOpen && <Button variant="secondary" onClick={() => onOpen()}>{t($ => $.artifacts.viewArtifact)}</Button>}
      <h3>{t($ => $.operations.files)}</h3>
      {artifact.files?.length ? <ul className="operation-files">{artifact.files.map(file => <li key={file.id}>
        <h4>{file.name}</h4><dl className="detail-list">
          <div><dt>{t($ => $.operations.fileId)}</dt><dd><code>{file.id}</code></dd></div>
          <div><dt>{t($ => $.operations.mediaType)}</dt><dd>{file.media_type}</dd></div>
          <div><dt>{t($ => $.operations.size)}</dt><dd>{t($ => $.operations.bytes, { size: file.size.toLocaleString() })}</dd></div>
          <div><dt>{t($ => $.operations.sha256)}</dt><dd><code>{file.sha256}</code></dd></div>
        </dl>
        {onOpen && <Button variant="secondary" onClick={() => onOpen(file.id)}>{t($ => $.artifacts.viewFile)}</Button>}
      </li>)}</ul> : <p className="catalog-muted">{t($ => $.operations.noFiles)}</p>}
    </div>
  </details>;
}

export function OperationDetails({ operation, onAnswer, onOpenArtifact, onOpenReport }: {
  operation: Operation;
  onAnswer?: (question: OperationQuestion) => void;
  onOpenArtifact?: (artifactID: string, fileID?: string) => void;
  onOpenReport?: () => void;
}) {
  const { t } = useTranslation();
  const progress = operation.progress;
  const artifacts = [...new Map(operation.tasks?.flatMap(task => task.result?.artifacts ?? []).map(artifact => [artifact.id, artifact])).values()];
  const events = [...operation.events ?? []].sort((a, b) => b.sequence - a.sequence);
  const affinity = operation.affinity;
  return <div className="catalog-details operation-details">
    <section className="catalog-section"><h2>{t($ => $.operations.summary)}</h2>
      <p className="operation-progress">{t($ => $.operations.progress, { total: progress.total_tasks, finished: progress.finished_tasks })}</p><p className="catalog-muted">{t($ => $.operations.progressNote)}</p>
      <dl className="detail-list">
        <div><dt>{t($ => $.operations.status)}</dt><dd><OperationStatus status={operation.status} /></dd></div>
        <div><dt>{t($ => $.operations.operationId)}</dt><dd><code>{operation.id}</code></dd></div>
        <div><dt>{t($ => $.operations.mission)}</dt><dd>{operation.mission.name}</dd></div>
        <div><dt>{t($ => $.operations.objectives)}</dt><dd>
          <ul className="catalog-tags">{operation.mission.objectives.map((objective, index) => <li key={index}>{objective}</li>)}</ul>
        </dd></div>
        <div><dt>{t($ => $.operations.createdAt)}</dt><dd><Timestamp value={operation.created_at} /></dd></div>
        <div><dt>{t($ => $.operations.updatedAt)}</dt><dd><Timestamp value={operation.updated_at} /></dd></div>
        {operation.report_id && <div><dt>{t($ => $.operations.reportId)}</dt><dd className="operation-report-reference"><code>{operation.report_id}</code>
          {onOpenReport && <Button variant="secondary" onClick={onOpenReport}>{t($ => $.artifacts.viewReport)}</Button>}
        </dd></div>}
      </dl>
      {operation.error && <div className="operation-error"><h3>{t($ => $.operations.error)}</h3><p>{operation.error}</p></div>}
      <OperationData label={t($ => $.operations.input)} value={operation.input} /><OperationData label={t($ => $.operations.output)} value={operation.output} />
    </section>
    {affinity && <Affinity affinity={affinity} />}
    {operation.preflight && <Preflight preflight={operation.preflight} />}
    <section className="catalog-section"><h2>{t($ => $.operations.tasks)}</h2>
      {operation.tasks?.length ? <ul className="operation-items">{operation.tasks.map(task => <li key={task.id}><Task task={task}
        queuedFor={isActiveOperation(operation.status) && task.status === 'queued' && !affinity?.error && affinity?.for_actions.includes(task.action_name)
          ? affinity.agent_id : undefined} /></li>)}</ul> : <p className="catalog-muted">{t($ => $.operations.noTasks)}</p>}
    </section>
    <section className="catalog-section"><h2>{t($ => $.operations.questions)}</h2>
      {operation.questions?.length ? <><p className="catalog-muted">{t($ => $.operations.questionStatusNote)}</p><ul className="operation-items">{operation.questions.map(question => <li key={question.id}><Question question={question}
        onAnswer={onAnswer && isActiveOperation(operation.status) ? () => onAnswer(question) : undefined} /></li>)}</ul></> : <p className="catalog-muted">{t($ => $.operations.noQuestions)}</p>}
    </section>
    <section className="catalog-section"><h2>{t($ => $.operations.artifacts)}</h2>
      {artifacts.length ? <ul className="operation-items">{artifacts.map(artifact => <li key={artifact.id}><Artifact artifact={artifact}
        onOpen={onOpenArtifact ? fileID => onOpenArtifact(artifact.id, fileID) : undefined} /></li>)}</ul> : <p className="catalog-muted">{t($ => $.operations.noArtifacts)}</p>}
    </section>
    <section className="catalog-section"><h2>{t($ => $.operations.events)}</h2>
      {events.length ? <details className="operation-card"><summary>{t($ => $.operations.eventHistory, { count: events.length })}</summary><div className="operation-card-body">
        <p className="catalog-muted">{t($ => $.operations.eventHistoryNote)}</p>
        <ol className="operation-events">{events.map(event => <li key={event.sequence}>
          <div className="operation-event-heading"><code>{event.type}</code><Timestamp value={event.created_at} /></div>
          {event.message && <p>{event.message}</p>}<div className="operation-event-meta"><span>{t($ => $.operations.eventSequence, { sequence: event.sequence })}</span><OperationStatus status={event.status} /></div>
          {event.task_id && <p>{t($ => $.operations.taskId)}: <code>{event.task_id}</code></p>}
          {event.question_id && <p>{t($ => $.operations.questionId)}: <code>{event.question_id}</code></p>}
        </li>)}</ol>
      </div></details> : <p className="catalog-muted">{t($ => $.operations.noEvents)}</p>}
    </section>
  </div>;
}

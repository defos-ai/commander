import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { LoaderCircle } from 'lucide-react';
import { CommandError, completeOperation } from '../api/operation-commands';
import type { Operation } from '../api/operation-types';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';
import { OperationCommandError } from './OperationCommandError';
import { isActiveOperation, OperationData, TaskStatus } from './OperationDetails';
import { SchemaDetails } from './SchemaDetails';
import '../styles/operation-commands.css';

function completionBlock(operation: Operation) {
  if (operation.tasks?.some(task => task.status === 'queued' || task.status === 'running')) return 'completionActiveTasks';
  if (operation.questions?.some(question => question.status !== 'answered')) return 'completionPendingQuestions';
  if (operation.tasks?.some(task => !['succeeded', 'failed', 'cancelled', 'interrupted'].includes(task.status))) return 'completionUnrecognizedWork';
  if (operation.tasks?.some(task => task.continuation && task.continuation.status !== 'resolved' &&
    (task.continuation.decision_by !== 'model' || !['pending', 'waiting'].includes(task.continuation.status)))) return 'completionUnresolvedWork';
  return undefined;
}

export function CompleteOperationDialog({ connection, credentials, operation, available, inspecting, readbackFailed,
  onClose, onSubmitted, onRejected, onCompleted, onInspect }: {
  connection: Connection;
  credentials: Session;
  operation: Operation;
  available: boolean;
  inspecting: boolean;
  readbackFailed: boolean;
  onClose: () => void;
  onSubmitted: () => void;
  onRejected: () => void;
  onCompleted: (operation: Operation) => void;
  onInspect: (id: string) => void;
}) {
  const { t } = useTranslation();
  const identity = useId();
  const proposal = operation.completion_proposal;
  const [output, setOutput] = useState(proposal?.outputText ?? '');
  const [loadedProposal, setLoadedProposal] = useState(() => proposal?.outputText === undefined ? undefined
    : { id: proposal.id, text: proposal.outputText });
  const [reviewed, setReviewed] = useState<{ updatedAt: string; proposalID?: string; proposalOutput?: string }>();
  const [validation, setValidation] = useState(false);
  const [error, setError] = useState<CommandError>();
  const [submitting, setSubmitting] = useState(false);
  const pending = useRef<AbortController | null>(null);
  const sent = useRef(false);
  const finished = !isActiveOperation(operation.status);
  const blocker = completionBlock(operation);
  const locked = Boolean(error && error.kind !== 'invalid_input');
  const approved = reviewed?.updatedAt === operation.updated_at && reviewed.proposalID === proposal?.id &&
    reviewed.proposalOutput === proposal?.outputText;
  const proposalChanged = proposal?.outputText !== undefined &&
    (proposal.id !== loadedProposal?.id || proposal.outputText !== loadedProposal?.text);
  const disabled = !available || finished || locked || submitting || inspecting;

  useEffect(() => () => { pending.current?.abort(); pending.current = null; }, [connection.id, connection.url, credentials, operation.id]);

  function close() { pending.current?.abort(); pending.current = null; onClose(); }

  function loadProposal() {
    if (disabled || proposal?.outputText === undefined) return;
    setOutput(proposal.outputText);
    setLoadedProposal({ id: proposal.id, text: proposal.outputText });
    setReviewed(undefined);
    setValidation(false);
    setError(undefined);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (sent.current || pending.current || disabled || blocker || !approved || !output.trim()) return;
    try { JSON.parse(output); }
    catch {
      setValidation(true);
      document.getElementById(`${identity}-output`)?.focus();
      return;
    }
    const controller = new AbortController();
    pending.current = controller;
    sent.current = true;
    setSubmitting(true);
    setError(undefined);
    onSubmitted();
    try {
      const completed = await completeOperation(connection.url, credentials, operation.id, output, controller.signal);
      if (!controller.signal.aborted && pending.current === controller) onCompleted(completed);
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) {
        const commandError = cause instanceof CommandError ? cause : new CommandError('unknown');
        if (commandError.kind === 'invalid_input') { sent.current = false; onRejected(); }
        setError(commandError);
      }
    } finally {
      if (!controller.signal.aborted && pending.current === controller) { pending.current = null; setSubmitting(false); }
    }
  }

  return <Dialog open title={t($ => $.operationCommands.completeTitle)} description={t($ => $.operationCommands.completeDescription)} onClose={close}>
    <form className="operation-command-form" onSubmit={submit} noValidate autoComplete="off">
      <p className="form-hint">{t($ => $.operationCommands.operationID)}: <code>{operation.id}</code></p>
      <section className="operation-completion-review">
        <h3>{t($ => $.operations.objectives)}</h3>
        <ul className="catalog-tags">{operation.mission.objectives.map((objective, index) => <li key={index}>{objective}</li>)}</ul>
        <p>{t($ => $.operations.progress, { total: operation.progress.total_tasks, finished: operation.progress.finished_tasks })}</p>
        <p className="form-hint">{t($ => $.operationCommands.completionLimitations)}</p>
        {operation.tasks?.length ? <details><summary>{t($ => $.operationCommands.reviewTaskOutcomes)}</summary>
          <ul className="operation-completion-tasks">{operation.tasks.map(task => <li key={task.id}>
            <span><code>{task.action_name}</code> <TaskStatus status={task.status} /></span>
            <p className="form-hint"><code>{task.id}</code></p>
            {task.error && <p>{task.error}</p>}
            <OperationData label={t($ => $.operations.result)} value={task.result?.output} />
          </li>)}</ul>
        </details> : <p className="form-hint">{t($ => $.operations.noTasks)}</p>}
      </section>
      {proposal && <section className="operation-completion-review">
        <h3>{t($ => $.operationCommands.savedProposal)}</h3>
        <p>{t($ => $.operationCommands.proposedBy)}: {t($ => $.operations.completionOrigins[proposal.proposed_by])}
          {' · '}<time dateTime={proposal.proposed_at}>{new Date(proposal.proposed_at).toLocaleString()}</time></p>
        <p className="form-hint">{t($ => $.operationCommands.proposalReviewHint)}</p>
        {proposalChanged && <p className="notice" role="status">{t($ => $.operationCommands.proposalChanged)}</p>}
        {proposal.outputText === undefined ? <p className="form-hint">{t($ => $.operationCommands.proposalNeedsRefresh)}</p>
          : (proposalChanged || output !== proposal.outputText) && <Button variant="secondary" disabled={disabled} onClick={loadProposal}>
            {t($ => $.operationCommands.loadProposal)}
          </Button>}
      </section>}
      <details className="operation-command-schema"><summary>{t($ => $.operationCommands.outputSchema)}</summary>
        <SchemaDetails schema={operation.mission.output_schema} direction="output" />
      </details>
      <div className="form-field"><label htmlFor={`${identity}-output`}>{t($ => $.operationCommands.finalOutput)}</label>
        <textarea id={`${identity}-output`} rows={8} value={output} spellCheck={false} autoFocus disabled={disabled}
          aria-invalid={validation || error?.code === 'invalid_output' || undefined}
          aria-describedby={validation ? `${identity}-validation` : undefined}
          onChange={event => { setOutput(event.target.value); setValidation(false); setReviewed(undefined); if (!locked) setError(undefined); }} />
      </div>
      {validation && <p className="form-error" id={`${identity}-validation`} role="alert">{t($ => $.operationCommands.invalidOutputJSON)}</p>}
      {finished ? <p className="notice" role="status">{t($ => $.operationCommands.finished)}</p>
        : !available ? <p className="notice" role="status">{t($ => $.operationCommands.refreshRequired)}</p>
          : blocker && <p className="notice" role="status">{t($ => $.operationCommands[blocker])}</p>}
      <label className="operation-completion-confirm"><input type="checkbox" checked={approved} disabled={disabled || Boolean(blocker)}
        onChange={event => setReviewed(event.target.checked ? { updatedAt: operation.updated_at, proposalID: proposal?.id,
          proposalOutput: proposal?.outputText } : undefined)} />{t($ => $.operationCommands.completeConfirm)}</label>
      {error && <OperationCommandError error={error} operationID={operation.id} onInspect={() => onInspect(operation.id)} />}
      {readbackFailed && <p className="notice" role="alert">{t($ => $.operationCommands.completionReadbackFailed)}</p>}
      {submitting && <p className="form-hint" role="status">{t($ => $.operationCommands.submittingNote)}</p>}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={close}>{t($ => $.operationCommands.close)}</Button>
        {finished && !error && <Button variant="secondary" disabled={inspecting} onClick={() => onInspect(operation.id)}>{t($ => $.operationCommands.viewOperation)}</Button>}
        <Button type="submit" disabled={disabled || Boolean(blocker) || !approved || !output.trim()} aria-busy={submitting}>
          {submitting && <LoaderCircle className="connection-form-spinner" size={16} aria-hidden="true" />}
          {submitting ? t($ => $.operationCommands.busy) : t($ => $.operationCommands.completeTitle)}
        </Button>
      </div>
    </form>
  </Dialog>;
}

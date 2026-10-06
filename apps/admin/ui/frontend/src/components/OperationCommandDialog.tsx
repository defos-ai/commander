import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { LoaderCircle } from 'lucide-react';
import { answerQuestion, cancelOperation, CommandError } from '../api/operation-commands';
import type { Operation, OperationQuestion } from '../api/operation-types';
import type { Connection, Credentials } from '../connections/types';
import { useQuestionExpiry } from '../hooks/useQuestionExpiry';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';
import { OperationCommandError } from './OperationCommandError';
import { isActiveOperation } from './OperationDetails';
import '../styles/operation-commands.css';

type Props = {
  connection: Connection;
  credentials: Credentials;
  operation: Operation;
  available: boolean;
  onClose: () => void;
  onCompleted: (operation: Operation) => void;
  onInspect: (id: string) => void;
} & ({ command: 'cancel' } | { command: 'answer'; question: OperationQuestion });

export function OperationCommandDialog(props: Props) {
  const { connection, credentials, operation, available, onClose, onCompleted, onInspect } = props;
  const { t } = useTranslation();
  const identity = useId();
  const [answer, setAnswer] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<CommandError>();
  const pending = useRef<AbortController | null>(null);
  const sent = useRef(false);
  const question = props.command === 'answer' ? props.question : undefined;
  const expired = useQuestionExpiry(question?.expires_at ?? '0001-01-01T00:00:00Z');
  const finished = !isActiveOperation(operation.status);
  const noLongerPending = question && question.status !== 'pending';
  const blocked = !available || finished || Boolean(question && (expired || noLongerPending)) || Boolean(error);

  useEffect(() => () => { pending.current?.abort(); pending.current = null; }, [connection.url, credentials, operation.id, question?.id]);

  function close() { pending.current?.abort(); pending.current = null; onClose(); }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (sent.current || pending.current || blocked || question && !answer.trim()) return;
    const controller = new AbortController();
    pending.current = controller;
    sent.current = true;
    setSubmitting(true);
    try {
      const result = question
        ? await answerQuestion(connection.url, credentials, operation.id, question.id, answer, controller.signal)
        : await cancelOperation(connection.url, credentials, operation.id, controller.signal);
      if (!controller.signal.aborted && pending.current === controller) onCompleted(result);
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) setError(cause instanceof CommandError ? cause : new CommandError('unknown'));
    } finally {
      if (!controller.signal.aborted && pending.current === controller) { pending.current = null; setSubmitting(false); }
    }
  }

  return <Dialog open title={question ? t($ => $.operationCommands.answerTitle) : t($ => $.operationCommands.cancelTitle)}
    description={question ? question.text : t($ => $.operationCommands.cancelDescription)} onClose={close}>
    <form className="operation-command-form" onSubmit={submit}>
      <p className="form-hint">{t($ => $.operationCommands.operationID)}: <code>{operation.id}</code></p>
      {question && <>
        {Boolean(question.options?.length) && <fieldset className="operation-command-options" disabled={submitting || blocked}>
          <legend>{t($ => $.operationCommands.options)}</legend>
          {question.options?.map((option, index) => <label key={index} className="operation-command-option"><input type="radio" name={`${identity}-option`} checked={answer === option}
            onChange={() => setAnswer(option)} />{option}</label>)}
        </fieldset>}
        <div className="form-field"><label htmlFor={`${identity}-answer`}>{t($ => $.operationCommands.answerLabel)}</label>
          <textarea id={`${identity}-answer`} rows={3} value={answer} required disabled={submitting || blocked}
            onChange={event => setAnswer(event.target.value)} />
          <p className="form-hint">{t($ => $.operationCommands.customAnswer)}</p>
        </div>
      </>}
      {finished ? <p className="notice" role="status">{t($ => $.operationCommands.finished)}</p>
        : noLongerPending ? <p className="notice" role="status">{t($ => $.operationCommands.noLongerPending)}</p>
          : question && expired ? <p className="notice" role="status">{t($ => $.operationCommands.expired)}</p>
            : !available && <p className="notice" role="status">{t($ => $.operationCommands.refreshRequired)}</p>}
      {error && <OperationCommandError error={error} operationID={operation.id} onInspect={() => onInspect(operation.id)} />}
      {submitting && <p className="form-hint" role="status">{t($ => $.operationCommands.submittingNote)}</p>}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={close}>{t($ => $.operationCommands.close)}</Button>
        {!error && <Button type="submit" className={question ? undefined : 'danger-button'} disabled={submitting || blocked || Boolean(question && !answer.trim())} aria-busy={submitting}>
          {submitting && <LoaderCircle className="connection-form-spinner" size={16} aria-hidden="true" />}
          {submitting ? t($ => $.operationCommands.busy) : question ? t($ => $.operationCommands.answerSubmit) : t($ => $.operationCommands.cancelSubmit)}
        </Button>}
      </div>
    </form>
  </Dialog>;
}

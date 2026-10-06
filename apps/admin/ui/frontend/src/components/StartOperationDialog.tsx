import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { LoaderCircle } from 'lucide-react';
import { ApiError } from '../api/client';
import { listMissions } from '../api/missions';
import { CommandError, startOperation } from '../api/operation-commands';
import type { Operation } from '../api/operation-types';
import type { Mission } from '../api/types';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';
import { OperationCommandError } from './OperationCommandError';
import { SchemaDetails } from './SchemaDetails';
import '../styles/operation-commands.css';

export function StartOperationDialog({ connection, credentials, onClose, onStarted, onInspect }: {
  connection: Connection;
  credentials: Session;
  onClose: () => void;
  onStarted: (operation: Operation) => void;
  onInspect: (id: string) => void;
}) {
  const { t } = useTranslation();
  const identity = useId();
  const [missions, setMissions] = useState<Mission[]>([]);
  const [missionName, setMissionName] = useState('');
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<ApiError>();
  const [loadRevision, setLoadRevision] = useState(0);
  const [input, setInput] = useState('{}');
  const [validation, setValidation] = useState<'invalidJSON' | 'idUnavailable'>();
  const [error, setError] = useState<CommandError>();
  const [submitting, setSubmitting] = useState(false);
  const pending = useRef<AbortController | null>(null);
  const blocked = useRef(false);
  const operationID = useRef<string | undefined>(undefined);
  const locked = error?.kind === 'unknown' || error?.code === 'operation_conflict';
  const mission = missions.find(item => item.name === missionName);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setLoadError(undefined);
    setMissions([]);
    void listMissions(connection.url, credentials, controller.signal).then(items => {
      if (controller.signal.aborted) return;
      setMissions(items);
      setMissionName(previous => items.some(item => item.name === previous) ? previous : items[0]?.name ?? '');
      setLoading(false);
    }).catch(cause => {
      if (controller.signal.aborted) return;
      setLoadError(cause instanceof ApiError ? cause : new ApiError('unavailable'));
      setLoading(false);
    });
    return () => controller.abort();
  }, [connection.url, credentials, loadRevision]);

  useEffect(() => () => pending.current?.abort(), [connection.url, credentials]);

  function close() {
    pending.current?.abort();
    onClose();
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending.current || blocked.current || !mission || loading) return;
    setValidation(undefined);
    setError(undefined);
    let value: unknown;
    try { value = JSON.parse(input); }
    catch {
      setValidation('invalidJSON');
      document.getElementById(`${identity}-input`)?.focus();
      return;
    }
    try { operationID.current ??= crypto.randomUUID(); }
    catch { setValidation('idUnavailable'); return; }
    const controller = new AbortController();
    pending.current = controller;
    setSubmitting(true);
    try {
      const operation = await startOperation(connection.url, credentials, {
        operation_id: operationID.current, mission_name: mission.name, input: value,
      }, controller.signal);
      if (!controller.signal.aborted && pending.current === controller) onStarted(operation);
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) {
        const commandError = cause instanceof CommandError ? cause : new CommandError('unknown');
        blocked.current = commandError.kind === 'unknown' || commandError.code === 'operation_conflict';
        setError(commandError);
      }
    } finally {
      if (!controller.signal.aborted && pending.current === controller) {
        pending.current = null;
        setSubmitting(false);
      }
    }
  }

  return <Dialog open title={t($ => $.operationCommands.startTitle)} description={t($ => $.operationCommands.startDescription)} onClose={close}>
    <form className="operation-command-form" onSubmit={submit} noValidate autoComplete="off">
      {loading ? <p className="catalog-loading" role="status"><LoaderCircle size={17} className="connection-form-spinner" aria-hidden="true" />{t($ => $.operationCommands.loadingMissions)}</p>
        : loadError ? <div className="notice" role="alert"><p>{t($ => $.operationCommands.missionErrors[loadError.kind])}</p>
          <Button variant="secondary" onClick={() => setLoadRevision(value => value + 1)}>{t($ => $.operationCommands.retry)}</Button>
        </div> : !missions.length ? <p className="form-hint">{t($ => $.operationCommands.noMissions)}</p> : <>
          <div className="form-field"><label htmlFor={`${identity}-mission`}>{t($ => $.operationCommands.mission)}</label>
            <select id={`${identity}-mission`} value={missionName} disabled={submitting || locked} autoFocus onChange={event => { setMissionName(event.target.value); setError(undefined); }}>
              <option value="" disabled>{t($ => $.operationCommands.chooseMission)}</option>
              {missions.map(item => <option key={item.name} value={item.name}>{item.name}</option>)}
            </select>
          </div>
          {mission && <p className="form-hint operation-command-objective">{mission.objective}</p>}
          {mission?.input_schema && <details className="operation-command-schema"><summary>{t($ => $.operationCommands.inputSchema)}</summary>
            <SchemaDetails schema={mission.input_schema} direction="input" />
          </details>}
          <div className="form-field"><label htmlFor={`${identity}-input`}>{t($ => $.operationCommands.missionInput)}</label>
            <textarea id={`${identity}-input`} rows={6} value={input} spellCheck={false} disabled={submitting || locked}
              aria-invalid={validation === 'invalidJSON' || undefined} aria-describedby={validation ? `${identity}-validation` : undefined}
              onChange={event => { setInput(event.target.value); setValidation(undefined); setError(undefined); }} />
          </div>
          <p className="form-hint">{t($ => $.operationCommands.startHint)}</p>
        </>}
      {validation && <p className="form-error" id={`${identity}-validation`} role="alert">{t($ => $.operationCommands[validation])}</p>}
      {error && <OperationCommandError error={error} operationID={locked ? operationID.current : undefined}
        onInspect={locked && operationID.current ? () => onInspect(operationID.current!) : undefined} />}
      {submitting && <div className="form-hint" role="status"><p>{t($ => $.operationCommands.submittingNote)}</p>
        <p className="operation-command-identity">{t($ => $.operationCommands.operationID)}: <code>{operationID.current}</code></p>
      </div>}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={close}>{t($ => $.operationCommands.close)}</Button>
        <Button type="submit" disabled={loading || !mission || submitting || locked} aria-busy={submitting}>
          {submitting && <LoaderCircle className="connection-form-spinner" size={16} aria-hidden="true" />}
          {submitting ? t($ => $.operationCommands.busy) : t($ => $.operationCommands.startSubmit)}
        </Button>
      </div>
    </form>
  </Dialog>;
}

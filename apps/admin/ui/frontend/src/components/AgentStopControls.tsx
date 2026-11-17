import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { LoaderCircle, Square } from 'lucide-react';
import { AgentStopError, listAgents, stopAgents, validAgentSelector, type AgentSelector, type AgentStopResult } from '../api/agents';
import { ApiError } from '../api/client';
import type { Agent } from '../api/types';
import type { Connection, Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { AgentStatus } from './AgentDetails';
import { Button } from './Button';
import { Dialog } from './Dialog';
import '../styles/agent-stop.css';

type Attempt = {
  credentials: Session;
  selector: AgentSelector;
  result?: AgentStopResult;
  error?: AgentStopError['kind'];
  saved?: Agent[];
};

export function AgentStopControls({ connection, credentials, selectedID, agent, available, onRefresh }: {
  connection: Connection; credentials: Session; selectedID?: string; agent?: Agent;
  available: boolean; onRefresh: () => void;
}) {
  const { t } = useTranslation();
  const [dialog, setDialog] = useState<{ credentials: Session; selectedID?: string }>();
  const [attempt, setAttempt] = useState<Attempt>();
  const [reading, setReading] = useState(false);
  const [readError, setReadError] = useState<ApiError['kind']>();
  const mutation = useRef<AbortController | null>(null);
  const read = useRef<AbortController | null>(null);
  const current = attempt?.credentials === credentials ? attempt : undefined;
  const stopError = current?.error;
  const canStop = available && (selectedID === undefined || agent?.id === selectedID && agent.status !== 'stop_requested' && agent.status !== 'stopped');

  function dismiss() {
    setDialog(undefined);
    if (mutation.current) {
      mutation.current.abort(); mutation.current = null;
      setAttempt(previous => previous && { ...previous, error: 'unknown' });
    }
  }

  useEffect(() => {
    setAttempt(undefined); setDialog(undefined); setReading(false); setReadError(undefined);
    return () => { mutation.current?.abort(); mutation.current = null; read.current?.abort(); read.current = null; };
  }, [connection.id, connection.url, credentials]);

  useEffect(() => {
    dismiss(); read.current?.abort(); read.current = null; setReading(false); setReadError(undefined);
  }, [selectedID]);

  async function submit(selector: AgentSelector) {
    if (!canStop || current || mutation.current || !validAgentSelector(selector)) return;
    const controller = new AbortController();
    mutation.current = controller;
    setAttempt({ credentials, selector });
    try {
      const result = await stopAgents(connection.url, credentials, selector, controller.signal);
      if (!controller.signal.aborted && mutation.current === controller) {
        setAttempt({ credentials, selector, result }); onRefresh();
      }
    } catch (cause) {
      if (!controller.signal.aborted && mutation.current === controller) {
        setAttempt({ credentials, selector, error: cause instanceof AgentStopError ? cause.kind : 'unknown' });
      }
    } finally {
      if (!controller.signal.aborted && mutation.current === controller) { mutation.current = null; setDialog(undefined); }
    }
  }

  async function inspectSaved() {
    if (!current || mutation.current || read.current) return;
    const controller = new AbortController();
    read.current = controller; setReading(true); setReadError(undefined);
    try {
      const inventory = await listAgents(connection.url, credentials, controller.signal);
      const ids = current.result ? new Set([...current.result.items, ...(current.result.errors ?? [])].map(item => item.id)) : undefined;
      const saved = inventory.filter(item => ids ? ids.has(item.id)
        : (!current.selector.id || current.selector.id === item.id) && (!current.selector.type || current.selector.type === item.type) &&
          (!current.selector.version || current.selector.version === item.version));
      if (!controller.signal.aborted && read.current === controller) { setAttempt({ ...current, saved }); onRefresh(); }
    } catch (cause) {
      if (!controller.signal.aborted && read.current === controller) setReadError(cause instanceof ApiError ? cause.kind : 'unavailable');
    } finally {
      if (!controller.signal.aborted && read.current === controller) { read.current = null; setReading(false); }
    }
  }

  const targets = current?.result ? [...current.result.items, ...(current.result.errors ?? [])] : [];
  return <div className="agent-stop-controls">
    <div className="agent-stop-actions"><Button variant="secondary" className="danger-button" disabled={!canStop || Boolean(current)} onClick={() => setDialog({ credentials, selectedID })}>
      <Square size={14} aria-hidden="true" />{selectedID === undefined ? t($ => $.agents.stop.multiple) : t($ => $.agents.stop.single)}
    </Button></div>
    {current && <section className="notice agent-stop-results" aria-label={t($ => $.agents.stop.results)}>
      <h2>{t($ => $.agents.stop.results)}</h2>
      <dl className="agent-stop-selector">{(['id', 'type', 'version'] as const).map(key => current.selector[key] && <div key={key}>
        <dt>{t($ => $.agents.stop.filters[key])}</dt><dd><code>{current.selector[key]}</code></dd>
      </div>)}</dl>
      <div role="status">
        {!current.result && !current.error && <p>{t($ => $.agents.stop.submitting)}</p>}
        {current.result && <p>{targets.length ? t($ => $.agents.stop.received) : t($ => $.agents.stop.noMatches)}</p>}
      </div>
      {stopError && <p role="alert">{t($ => $.agents.stop.errors[stopError])}</p>}
      {targets.length > 0 && <ul>{targets.map(item => <li key={item.id}>
        <code>{item.id}</code>
        {'status' in item ? <AgentStatus status={item.status} /> : <span>{t($ => $.agents.stop.targetErrors[item.error])}</span>}
        {current.saved !== undefined && <span>{t($ => $.agents.stop.saved)}: {current.saved.find(saved => saved.id === item.id)
          ? <AgentStatus status={current.saved.find(saved => saved.id === item.id)!.status} /> : t($ => $.agents.stop.notRetained)}</span>}
      </li>)}</ul>}
      {current.saved !== undefined && !current.result && <ul>{current.saved.map(item => <li key={item.id}><code>{item.id}</code><AgentStatus status={item.status} /></li>)}</ul>}
      {current.saved !== undefined && <p role="status">{current.result ? t($ => $.agents.stop.readbackNote) : t($ => $.agents.stop.unknownReadbackNote)}</p>}
      {current.saved === undefined && <p>{t($ => $.agents.stop.readbackRequired)}</p>}
      <p>{t($ => $.agents.stop.drainNote)}</p>
      {readError && <p role="alert">{t($ => $.agents.errors[readError])}</p>}
      <div className="agent-stop-actions">
        <Button variant="secondary" disabled={Boolean(mutation.current) || reading} onClick={() => void inspectSaved()}>
          {reading ? t($ => $.agents.stop.reading) : t($ => $.agents.stop.readSaved)}
        </Button>
        {current.saved !== undefined && <Button variant="secondary" disabled={!canStop || reading} onClick={() => { setAttempt(undefined); setReadError(undefined); setDialog({ credentials, selectedID }); }}>
          {t($ => $.agents.stop.newRequest)}
        </Button>}
      </div>
    </section>}
    {dialog?.credentials === credentials && dialog.selectedID === selectedID && <AgentStopDialog selectedID={selectedID} submitting={Boolean(current)} available={canStop} onClose={dismiss} onSubmit={selector => void submit(selector)} />}
  </div>;
}

function AgentStopDialog({ selectedID, submitting, available, onClose, onSubmit }: {
  selectedID?: string; submitting: boolean; available: boolean; onClose: () => void; onSubmit: (selector: AgentSelector) => void;
}) {
  const { t } = useTranslation();
  const identity = useId();
  const [id, setID] = useState(selectedID ?? '');
  const [type, setType] = useState('');
  const [version, setVersion] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [invalid, setInvalid] = useState(false);
  const selector: AgentSelector = { ...(id ? { id } : {}), ...(type ? { type } : {}), ...(version ? { version } : {}) };
  const valid = validAgentSelector(selector);

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!valid) { setInvalid(true); return; }
    if (confirmed && !submitting && available) onSubmit(selector);
  }

  return <Dialog open title={selectedID === undefined ? t($ => $.agents.stop.multiple) : t($ => $.agents.stop.single)} description={t($ => $.agents.stop.description)} onClose={onClose}>
    <form className="agent-stop-form" onSubmit={submit}>
      <div className="form-field"><label htmlFor={`${identity}-id`}>{t($ => $.agents.stop.filters.id)}</label>
        <input id={`${identity}-id`} value={id} readOnly={selectedID !== undefined} disabled={submitting} autoCapitalize="none" spellCheck={false} onChange={event => { setID(event.target.value); setConfirmed(false); setInvalid(false); }} />
      </div>
      {selectedID === undefined && <>
        <div className="form-field"><label htmlFor={`${identity}-type`}>{t($ => $.agents.stop.filters.type)}</label>
          <input id={`${identity}-type`} value={type} disabled={submitting} autoCapitalize="none" spellCheck={false} onChange={event => { setType(event.target.value); setConfirmed(false); setInvalid(false); }} />
        </div>
        <div className="form-field"><label htmlFor={`${identity}-version`}>{t($ => $.agents.stop.filters.version)}</label>
          <input id={`${identity}-version`} value={version} disabled={submitting} autoCapitalize="none" spellCheck={false} onChange={event => { setVersion(event.target.value); setConfirmed(false); setInvalid(false); }} />
        </div>
        <p className="form-hint">{t($ => $.agents.stop.filterHint)}</p>
      </>}
      <p className="form-hint">{t($ => $.agents.stop.drainNote)}</p>
      {invalid && <p className="notice" role="alert">{t($ => $.agents.stop.errors.invalid_input)}</p>}
      <label className="agent-stop-confirmation"><input type="checkbox" checked={confirmed} disabled={submitting || !valid} onChange={event => setConfirmed(event.target.checked)} />{t($ => $.agents.stop.confirm)}</label>
      <div className="dialog-actions"><Button variant="secondary" onClick={onClose}>{t($ => $.agents.stop.cancel)}</Button>
        <Button type="submit" className="danger-button" disabled={!confirmed || !valid || submitting || !available} aria-busy={submitting}>
          {submitting && <LoaderCircle className="connection-form-spinner" size={16} aria-hidden="true" />}{submitting ? t($ => $.agents.stop.submitting) : t($ => $.agents.stop.submit)}
        </Button>
      </div>
    </form>
  </Dialog>;
}

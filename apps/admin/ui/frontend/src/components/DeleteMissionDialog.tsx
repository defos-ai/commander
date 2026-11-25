import { useEffect, useRef, useState } from 'react';
import { LoaderCircle, Trash2 } from 'lucide-react';
import { ApiError } from '../api/client';
import { deleteMission, listMissions, MissionDeleteError } from '../api/missions';
import type { Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';

export function DeleteMissionDialog({ url, credentials, name, disabled, onDeleted }: {
  url: string;
  credentials: Session;
  name: string;
  disabled: boolean;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<MissionDeleteError>();
  const [readError, setReadError] = useState<ApiError>();
  const [needsReadback, setNeedsReadback] = useState(false);
  const [stillPresent, setStillPresent] = useState(false);
  const pending = useRef<AbortController | null>(null);
  const sent = useRef(false);

  useEffect(() => {
    setOpen(false);
    setBusy(false);
    setError(undefined);
    setReadError(undefined);
    setNeedsReadback(false);
    setStillPresent(false);
    sent.current = false;
    return () => { pending.current?.abort(); pending.current = null; };
  }, [url, credentials, name]);

  function close() {
    if (pending.current) {
      pending.current.abort();
      pending.current = null;
      if (sent.current) { setError(new MissionDeleteError('unknown')); setNeedsReadback(true); }
    }
    setBusy(false);
    setOpen(false);
  }

  async function remove() {
    if (pending.current || sent.current || needsReadback || disabled) return;
    const controller = new AbortController();
    pending.current = controller;
    sent.current = true;
    setBusy(true);
    setStillPresent(false);
    setReadError(undefined);
    try {
      await deleteMission(url, credentials, name, controller.signal);
      if (!controller.signal.aborted && pending.current === controller) onDeleted();
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) {
        setError(cause instanceof MissionDeleteError ? cause : new MissionDeleteError('unknown'));
        setNeedsReadback(true);
      }
    } finally {
      if (!controller.signal.aborted && pending.current === controller) { pending.current = null; setBusy(false); }
    }
  }

  async function inspect() {
    if (pending.current) return;
    const controller = new AbortController();
    pending.current = controller;
    setBusy(true);
    setReadError(undefined);
    try {
      // A validated catalog distinguishes absence from an unrelated proxy 404.
      const missions = await listMissions(url, credentials, controller.signal);
      if (controller.signal.aborted || pending.current !== controller) return;
      if (!missions.some(mission => mission.name === name)) { onDeleted(); return; }
      sent.current = false;
      setNeedsReadback(false);
      setError(undefined);
      setStillPresent(true);
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) setReadError(cause instanceof ApiError ? cause : new ApiError('unavailable'));
    } finally {
      if (!controller.signal.aborted && pending.current === controller) { pending.current = null; setBusy(false); }
    }
  }

  return <>
    <Button variant="secondary" className="danger-button" disabled={disabled} onClick={() => setOpen(true)}>
      <Trash2 size={15} aria-hidden="true" />{t($ => $.missions.deleteTitle)}
    </Button>
    {open && <Dialog open title={t($ => $.missions.deleteTitle)} description={t($ => $.missions.deleteDescription)} onClose={close}>
      <p className="mission-delete-name">{name}</p>
      {error && <div className="notice" role="alert"><p>{t($ => $.missions.deleteErrors[error.kind])}</p></div>}
      {readError && <div className="notice" role="alert"><p>{t($ => $.missions.errors[readError.kind])}</p></div>}
      {stillPresent && <p role="status">{t($ => $.missions.deleteStillPresent)}</p>}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={close}>{t($ => $.missions.cancel)}</Button>
        {needsReadback ? <Button disabled={busy} onClick={() => void inspect()} aria-busy={busy}>
          {busy && <LoaderCircle size={16} className="connection-form-spinner" aria-hidden="true" />}{t($ => $.missions.checkSaved)}
        </Button> : <Button className="danger-button" disabled={busy || disabled} onClick={() => void remove()} aria-busy={busy}>
          {busy && <LoaderCircle size={16} className="connection-form-spinner" aria-hidden="true" />}{busy ? t($ => $.missions.deleting) : t($ => $.missions.deleteTitle)}
        </Button>}
      </div>
    </Dialog>}
  </>;
}

import { useRef, useState } from 'react';
import { Button } from '../components/Button';
import type { Connection } from '../connections/types';
import { useTranslation } from '../i18n';

export function ConnectionSettingsPage({ connection, signedIn, onEdit, onForget, onRemove }: {
  connection: Connection;
  signedIn: boolean;
  onEdit: () => void;
  onForget: () => Promise<void>;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const editButton = useRef<HTMLButtonElement>(null);
  return <section>
    <div className="page-heading"><div><h1>{t($ => $.settings.title)}</h1><p>{t($ => $.settings.description)}</p></div></div>
    <dl className="connection-details">
      <div><dt>{t($ => $.settings.name)}</dt><dd>{connection.name}</dd></div>
      <div><dt>{t($ => $.settings.url)}</dt><dd>{connection.url}</dd></div>
      <div><dt>{t($ => $.settings.credentials)}</dt><dd>{signedIn ? t($ => $.settings.remembered) : t($ => $.settings.notRemembered)}</dd></div>
    </dl>
    {failed && <p className="notice" role="alert">{t($ => $.settings.signOutError)}</p>}
    <div className="settings-actions">
      <Button ref={editButton} onClick={onEdit}>{t($ => $.settings.edit)}</Button>
      {signedIn && <Button variant="secondary" disabled={busy} onClick={async () => {
        setBusy(true); setFailed(false);
        try { await onForget(); editButton.current?.focus(); } catch { setFailed(true); } finally { setBusy(false); }
      }}>{t($ => $.settings.forget)}</Button>}
      <Button variant="secondary" className="danger-button" onClick={onRemove}>{t($ => $.settings.remove)}</Button>
    </div>
  </section>;
}

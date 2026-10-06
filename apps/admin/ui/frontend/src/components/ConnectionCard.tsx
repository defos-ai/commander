import { useId } from 'react';
import { ArrowUpRight, Pencil, Server, Trash2 } from 'lucide-react';
import type { Connection, ConnectionStatus } from '../connections/types';
import { useTranslation } from '../i18n';

export function ConnectionCard({ connection, status, onOpen, onEdit, onRemove }: {
  connection: Connection;
  status: ConnectionStatus;
  onOpen: (id: string) => void;
  onEdit?: (id: string) => void;
  onRemove?: (id: string) => void;
}) {
  const { t } = useTranslation();
  const urlID = useId();
  const statusID = useId();
  return (
    <li className="connection-tile">
      <button className="connection-card" type="button" onClick={() => onOpen(connection.id)} aria-label={t($ => $.connections.open, { name: connection.name })} aria-describedby={`${urlID} ${statusID}`}>
        <span className="connection-card-top"><Server size={21} strokeWidth={1.3} aria-hidden="true" /><ArrowUpRight size={19} strokeWidth={1.4} aria-hidden="true" /></span>
        <span className="connection-card-name">{connection.name}</span>
        <span className="connection-card-url" id={urlID}>{connection.url}</span>
        <span className="connection-card-status" data-status={status} id={statusID}>
          <span className="status-dot" aria-hidden="true" />{t($ => $.connections.status[status])}
        </span>
      </button>
      {(onEdit || onRemove) && <div className="connection-card-actions">
        {onEdit && <button className="icon-button" type="button" aria-label={t($ => $.connections.edit, { name: connection.name })} onClick={() => onEdit(connection.id)}><Pencil size={15} aria-hidden="true" /></button>}
        {onRemove && <button className="icon-button" type="button" aria-label={t($ => $.connections.remove, { name: connection.name })} onClick={() => onRemove(connection.id)}><Trash2 size={15} aria-hidden="true" /></button>}
      </div>}
    </li>
  );
}

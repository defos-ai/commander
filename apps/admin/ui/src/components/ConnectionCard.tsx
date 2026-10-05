import { useId } from 'react';
import { ArrowUpRight, Server } from 'lucide-react';
import type { Connection, ConnectionStatus } from '../connections/types';
import { useTranslation } from '../i18n';

export function ConnectionCard({ connection, status, onOpen }: {
  connection: Connection;
  status: ConnectionStatus;
  onOpen: (id: string) => void;
}) {
  const { t } = useTranslation();
  const urlID = useId();
  const statusID = useId();
  return (
    <li>
      <button className="connection-card" type="button" onClick={() => onOpen(connection.id)} aria-label={t($ => $.connections.open, { name: connection.name })} aria-describedby={`${urlID} ${statusID}`}>
        <span className="connection-card-top"><Server size={21} strokeWidth={1.3} aria-hidden="true" /><ArrowUpRight size={19} strokeWidth={1.4} aria-hidden="true" /></span>
        <span className="connection-card-name">{connection.name}</span>
        <span className="connection-card-url" id={urlID}>{connection.url}</span>
        <span className="connection-card-status" data-status={status} id={statusID}>
          <span className="status-dot" aria-hidden="true" />{t($ => $.connections.status[status])}
        </span>
      </button>
    </li>
  );
}

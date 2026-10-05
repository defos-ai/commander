import { Plus, Server } from 'lucide-react';
import { Brand } from '../components/Brand';
import { Button } from '../components/Button';
import { ConnectionCard } from '../components/ConnectionCard';
import type { Connection, ConnectionStatus } from '../connections/types';
import { useTranslation } from '../i18n';

type ConnectionsPageProps = {
  statuses?: Record<string, ConnectionStatus>;
  onAdd?: () => void;
} & ({ connections?: never; onOpen?: never } | { connections: Connection[]; onOpen: (id: string) => void });

export function ConnectionsPage({ connections = [], statuses = {}, onAdd, onOpen }: ConnectionsPageProps) {
  const { t } = useTranslation();
  return (
    <div className="connections-page">
      <header className="connections-hero">
        <h1><Brand /></h1>
        <p className="connections-introduction">{t($ => $.connections.introduction)}</p>
      </header>
      <main className="connections-main" aria-labelledby="connections-heading">
        <div className="connections-toolbar">
          <div><h2 id="connections-heading">{t($ => $.connections.title)}</h2><span className="connections-count">{t($ => $.connections.count, { count: connections.length })}</span></div>
          {onAdd && <Button onClick={onAdd}><Plus size={16} strokeWidth={1.6} aria-hidden="true" />{t($ => $.connections.add)}</Button>}
        </div>
        {connections.length > 0 && onOpen ? (
          <ul className="connections-grid">{connections.map(connection => <ConnectionCard key={connection.id} connection={connection} status={statuses[connection.id] ?? 'unchecked'} onOpen={onOpen} />)}</ul>
        ) : (
          <div className="connections-empty">
            <span className="empty-icon"><Server size={25} strokeWidth={1.2} aria-hidden="true" /></span>
            <h3>{t($ => $.connections.emptyTitle)}</h3>
            <p>{t($ => $.connections.emptyDescription)}</p>
          </div>
        )}
      </main>
      <footer className="connections-footer">{t($ => $.connections.footer)}</footer>
    </div>
  );
}

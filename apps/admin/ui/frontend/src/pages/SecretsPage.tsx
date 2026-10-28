import { useId, useState } from 'react';
import { ArrowLeft, LoaderCircle, Plus, RefreshCw } from 'lucide-react';
import { SecretError, type PendingSecretRequest } from '../api/secrets';
import { Button } from '../components/Button';
import { SecretDialog, type SecretDialogTarget } from '../components/SecretDialog';
import type { Connection, Session } from '../connections/types';
import { useSecrets } from '../hooks/useSecrets';
import { useTranslation } from '../i18n';
import '../styles/catalog.css';
import '../styles/secrets.css';

type Props = { connection: Connection; credentials?: Session; onSignIn: () => void; onBack?: () => void };
type RequestGroup = {
  name: string;
  agents: Set<string>;
  status: PendingSecretRequest['status'];
};

function groupRequests(requests: PendingSecretRequest[]): RequestGroup[] {
  const groups = new Map<string, RequestGroup>();
  const priority = { needs_value: 0, awaiting_delivery: 1, awaiting_installation: 2 };
  for (const request of requests) {
    const key = request.name;
    const group = groups.get(key);
    if (group) {
      group.agents.add(request.agent_id);
      if (priority[request.status] < priority[group.status]) group.status = request.status;
    } else groups.set(key, { name: request.name, status: request.status, agents: new Set([request.agent_id]) });
  }
  return [...groups.values()].sort((left, right) => left.name.localeCompare(right.name));
}

export function SecretsPage(props: Props) {
  // Connection changes discard local form state before another API is rendered.
  return <SecretsContent key={`${props.connection.id}\0${props.connection.url}`} {...props} />;
}

function SecretsContent({ connection, credentials, onSignIn, onBack }: Props) {
  const { t } = useTranslation();
  const identity = useId();
  const [dialog, setDialog] = useState<{ credentials: Session; target: SecretDialogTarget }>();
  const [notice, setNotice] = useState<'saved' | 'deleted'>();
  const query = useSecrets({ url: connection.url, credentials });
  const groups = groupRequests(query.requests ?? []);
  const error = !credentials ? new SecretError('authentication') : query.error;
  const fatal = [error, query.listError].find(value => value && ['authentication', 'forbidden', 'disabled'].includes(value.kind));
  const canManage = Boolean(credentials && !query.loading && !error && !query.listError);

  function open(target: SecretDialogTarget) {
    if (!credentials || !canManage) return;
    setNotice(undefined);
    setDialog({ credentials, target });
  }

  function errorNotice(value: SecretError) {
    return <div className="notice" role="alert"><p>{t($ => $.secrets.errors[value.kind])}</p>
      {value.kind === 'authentication' ? <Button onClick={onSignIn}>{t($ => $.secrets.signIn)}</Button>
        : value.kind !== 'disabled' && value.kind !== 'forbidden' && <Button variant="secondary" onClick={query.refresh}>{t($ => $.secrets.retry)}</Button>}
    </div>;
  }

  return <section className="catalog-page secrets-page">
    {onBack && <button className="catalog-back" type="button" onClick={onBack}>
      <ArrowLeft size={16} aria-hidden="true" />{t($ => $.agents.back)}
    </button>}
    <div className="page-heading"><div><h1>{t($ => $.secrets.title)}</h1><p>{t($ => $.secrets.description)}</p></div>
      <div className="secret-actions">
        {credentials && <Button variant="secondary" onClick={query.refresh} disabled={query.refreshing}>
          <RefreshCw size={15} aria-hidden="true" />{query.refreshing ? t($ => $.secrets.refreshing) : t($ => $.secrets.refresh)}
        </Button>}
        <Button onClick={() => open({ mode: 'create' })} disabled={!canManage}><Plus size={15} aria-hidden="true" />{t($ => $.secrets.add)}</Button>
      </div>
    </div>
    {notice && <p className="notice" role="status">{t($ => $.secrets[notice])}</p>}
    {fatal ? errorNotice(fatal) : <>
      <section className="secret-section" aria-labelledby={`${identity}-pending`}>
        <div className="secret-section-heading"><h2 id={`${identity}-pending`}>{t($ => $.secrets.pendingTitle)}</h2><p>{t($ => $.secrets.pendingDescription)}</p></div>
        {error ? errorNotice(error) : query.loading ? <p className="catalog-loading" role="status">
          <LoaderCircle size={17} className="connection-form-spinner" aria-hidden="true" />{t($ => $.secrets.loading)}</p>
          : groups.length === 0 ? <p className="secret-empty">{t($ => $.secrets.noPending)}</p>
            : <ul className="secret-list">{groups.map(group => <li className="secret-row" key={group.name}>
              <div className="secret-row-main"><p className="secret-key"><strong>{group.name}</strong></p>
                <p className="catalog-row-meta"><span className="catalog-status" data-tone={group.status === 'needs_value' ? 'warning' : undefined}>{t($ => $.secrets.pendingStatus[group.status])}</span>
                  <span>{t($ => $.secrets.agents, { count: group.agents.size })}</span></p>
              </div>
              {group.status === 'needs_value' && <Button variant="secondary" disabled={!canManage}
                onClick={() => open({ mode: 'provide', key: { name: group.name } })}>{t($ => $.secrets.provide)}</Button>}
            </li>)}</ul>}
      </section>
      <section className="secret-section" aria-labelledby={`${identity}-saved`}>
        <h2 id={`${identity}-saved`}>{t($ => $.secrets.savedTitle)}</h2>
        {query.listError ? errorNotice(query.listError) : query.loading ? <p className="catalog-loading" role="status">{t($ => $.secrets.loading)}</p>
            : !query.items?.length ? <p className="secret-empty">{t($ => $.secrets.noSaved)}</p>
              : <ul className="secret-list" aria-label={t($ => $.secrets.savedTitle)}>{query.items.map(secret => <li className="secret-row" key={secret.id}>
                <div className="secret-row-main"><p className="secret-key"><strong>{secret.name}</strong></p>
                  <p className="catalog-row-meta"><span className="catalog-status" data-tone={secret.status === 'available' ? 'ready' : 'warning'}>{t($ => $.secrets.status[secret.status])}</span>
                    <span>{t($ => $.secrets.version, { version: secret.version })}</span>
                    <span>{secret.expires_at ? t($ => $.secrets.expires, { time: new Date(secret.expires_at).toLocaleString() }) : t($ => $.secrets.noExpiry)}</span>
                  </p>
                </div>
                <div className="secret-actions"><Button variant="secondary" disabled={!canManage} aria-label={t($ => $.secrets.replaceLabel, { name: secret.name })}
                  onClick={() => open({ mode: 'replace', secret })}>{t($ => $.secrets.replace)}</Button>
                  <Button variant="secondary" className="danger-button" disabled={!canManage} aria-label={t($ => $.secrets.deleteLabel, { name: secret.name })}
                    onClick={() => open({ mode: 'delete', secret })}>{t($ => $.secrets.delete)}</Button></div>
              </li>)}</ul>}
      </section>
      {credentials && !query.loading && !error && <p className="catalog-updated">{t($ => $.secrets.polling)} {query.checkedAt && t($ => $.secrets.checked, { time: new Date(query.checkedAt).toLocaleTimeString() })}</p>}
    </>}
    {credentials && dialog?.credentials === credentials && <SecretDialog connection={connection} credentials={credentials} target={dialog.target}
      onClose={() => setDialog(undefined)} onRefresh={query.refresh} onCompleted={deleted => {
        setDialog(undefined); setNotice(deleted ? 'deleted' : 'saved'); query.refresh();
      }} />}
  </section>;
}

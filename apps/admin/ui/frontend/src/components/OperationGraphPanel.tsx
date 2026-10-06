import { LoaderCircle, RefreshCw } from 'lucide-react';
import type { Credentials } from '../connections/types';
import { useOperationGraph } from '../hooks/useOperationGraph';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { ExecutionGraph } from './ExecutionGraph';
import { OperationStatus } from './OperationDetails';

export function OperationGraphPanel({ url, credentials, operationID, onSignIn, onOpenArtifact }: {
  url: string;
  credentials?: Credentials;
  operationID: string;
  onSignIn: () => void;
  onOpenArtifact?: (artifactID: string, fileID?: string) => void;
}) {
  const { t } = useTranslation();
  const query = useOperationGraph(url, credentials, operationID);
  const error = !credentials ? 'authentication' : query.error?.kind;
  return <section className="operation-graph-panel" aria-label={t($ => $.operationGraph.title)}>
    <div className="operation-graph-heading"><div>
      <h2>{t($ => $.operationGraph.title)}</h2><p className="catalog-muted">{t($ => $.operationGraph.description)}</p>
    </div>
      {credentials && <Button variant="secondary" disabled={query.refreshing} onClick={query.refresh}>
        <RefreshCw size={15} aria-hidden="true" />{query.refreshing ? t($ => $.operationGraph.refreshing) : t($ => $.operationGraph.refresh)}
      </Button>}
    </div>
    {error ? <div className="notice" role="alert"><p>{t($ => $.operationGraph.errors[error])}</p>
      {error === 'authentication' && <Button onClick={onSignIn}>{t($ => $.operationGraph.signIn)}</Button>}
    </div> : query.graph ? <>
      <p className="operation-graph-snapshot"><OperationStatus status={query.graph.status} />
        <time dateTime={query.graph.updated_at}>{t($ => $.operationGraph.updated, { time: new Date(query.graph.updated_at).toLocaleString() })}</time>
      </p>
      <ExecutionGraph key={`${url}:${operationID}`} graph={query.graph} onOpenArtifact={onOpenArtifact} />
      <p className="catalog-updated">{t($ => $.operationGraph.polling)}</p>
    </> : <p className="catalog-loading" role="status"><LoaderCircle className="connection-form-spinner" size={17} aria-hidden="true" />{t($ => $.operationGraph.loading)}</p>}
  </section>;
}

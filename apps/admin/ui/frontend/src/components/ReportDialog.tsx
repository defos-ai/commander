import { useEffect, useState } from 'react';
import { Download, LoaderCircle } from 'lucide-react';
import { ApiError } from '../api/client';
import { getOperationReport, type Report } from '../api/reports';
import type { Session } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';
import { useBlobDownload } from './artifact-download';
import '../styles/artifacts.css';

type Source = { url: string; credentials: Session; id: string; revision: number; report?: Report; error?: ApiError };

export function ReportDialog({ url, credentials, operationID, onClose }: {
  url: string; credentials: Session; operationID: string; onClose: () => void;
}) {
  const { t } = useTranslation();
  const [source, setSource] = useState<Source>();
  const [revision, setRevision] = useState(0);
  const download = useBlobDownload();
  const current = source?.url === url && source.credentials === credentials && source.id === operationID && source.revision === revision ? source : undefined;
  useEffect(() => {
    const controller = new AbortController();
    const identity = { url, credentials, id: operationID, revision };
    setSource(identity);
    void getOperationReport(url, credentials, operationID, controller.signal).then(report => {
      if (!controller.signal.aborted) setSource({ ...identity, report });
    }).catch(cause => {
      if (!controller.signal.aborted) setSource({ ...identity, error: cause instanceof ApiError ? cause : new ApiError('unavailable') });
    });
    return () => controller.abort();
  }, [url, credentials, operationID, revision]);
  const report = current?.report;
  const errorKind = current?.error?.kind;
  return <Dialog open className="artifact-dialog" title={t($ => $.artifacts.reportTitle)} onClose={onClose}>
    {!current || !report && !current.error ? <p className="artifact-loading" role="status"><LoaderCircle size={17} className="connection-form-spinner" aria-hidden="true" />{t($ => $.artifacts.loadingReport)}</p>
      : errorKind ? <div className="notice" role="alert"><p>{t($ => $.artifacts.errors[errorKind])}</p><Button variant="secondary" onClick={() => setRevision(value => value + 1)}>{t($ => $.artifacts.retry)}</Button></div>
        : report && <div className="artifact-content"><dl className="detail-list">
          <div><dt>{t($ => $.operations.reportId)}</dt><dd><code>{report.id}</code></dd></div>
          <div><dt>{t($ => $.operations.operationId)}</dt><dd><code>{report.operation_id}</code></dd></div>
          <div><dt>{t($ => $.operations.mission)}</dt><dd>{report.mission}</dd></div>
          <div><dt>{t($ => $.operations.createdAt)}</dt><dd><time dateTime={report.created_at}>{new Date(report.created_at).toLocaleString()}</time></dd></div>
        </dl><section className="artifact-preview" aria-label={t($ => $.artifacts.reportOutput)}><h3>{t($ => $.operations.output)}</h3>
          <pre tabIndex={0}>{typeof report.output === 'string' ? report.output : JSON.stringify(report.output, null, 2)}</pre>
        </section></div>}
    <div className="dialog-actions"><Button variant="secondary" onClick={onClose}>{t($ => $.artifacts.close)}</Button>
      {report && <Button onClick={() => download(new Blob([JSON.stringify(report, null, 2) + '\n'], { type: 'application/json' }), `${report.id}.json`)}><Download size={15} aria-hidden="true" />{t($ => $.artifacts.downloadJSON)}</Button>}
    </div>
  </Dialog>;
}

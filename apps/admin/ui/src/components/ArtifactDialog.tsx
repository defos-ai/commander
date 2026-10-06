import { useEffect, useRef, useState } from 'react';
import { Download, LoaderCircle } from 'lucide-react';
import { FileTooLargeError, getArtifact, getArtifactFile, MAX_FILE_BYTES } from '../api/artifacts';
import { ApiError } from '../api/client';
import type { OperationArtifact, OperationArtifactFile } from '../api/operation-types';
import type { Credentials } from '../connections/types';
import { useTranslation } from '../i18n';
import { Button } from './Button';
import { Dialog } from './Dialog';
import { useBlobDownload } from './artifact-download';
import '../styles/artifacts.css';

const PREVIEW_BYTES = 2 * 1024 * 1024;
const rasterTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];

function previewKind(file: OperationArtifactFile) {
  const type = file.media_type.split(';')[0].trim().toLowerCase();
  if (rasterTypes.includes(type)) return 'image';
  if (type.startsWith('text/') || type === 'application/json' || type.endsWith('+json') || type === 'application/xml' || type.endsWith('+xml')) return 'text';
  return undefined;
}

function matchesRaster(bytes: Uint8Array, type: string) {
  const starts = (...prefix: number[]) => prefix.every((value, index) => bytes[index] === value);
  const text = (start: number, end: number) => String.fromCharCode(...bytes.subarray(start, end));
  if (type === 'image/png') return starts(137, 80, 78, 71, 13, 10, 26, 10);
  if (type === 'image/jpeg') return starts(255, 216, 255);
  if (type === 'image/gif') return text(0, 6) === 'GIF87a' || text(0, 6) === 'GIF89a';
  return type === 'image/webp' && text(0, 4) === 'RIFF' && text(8, 12) === 'WEBP';
}

type FileMode = 'preview' | 'download';
type Preview = { file: OperationArtifactFile; kind: 'text'; text: string } | { file: OperationArtifactFile; kind: 'image'; url: string };
type Source = { url: string; credentials: Credentials; id: string; revision: number; artifact?: OperationArtifact; error?: ApiError };

export function ArtifactDialog({ url, credentials, artifactID, initialFileID, onClose }: {
  url: string; credentials: Credentials; artifactID: string; initialFileID?: string; onClose: () => void;
}) {
  const { t } = useTranslation();
  const [source, setSource] = useState<Source>();
  const [revision, setRevision] = useState(0);
  const [preview, setPreview] = useState<Preview>();
  const [busy, setBusy] = useState<{ file: OperationArtifactFile; mode: FileMode }>();
  const [fileError, setFileError] = useState<{ file: OperationArtifactFile; mode: FileMode; error: ApiError | FileTooLargeError | 'invalidPreview' }>();
  const pending = useRef<AbortController | null>(null);
  const imageURL = useRef<string | null>(null);
  const previewElement = useRef<HTMLElement>(null);
  const download = useBlobDownload();
  const current = source?.url === url && source.credentials === credentials && source.id === artifactID && source.revision === revision ? source : undefined;
  const errorKind = current?.error?.kind;
  const fileErrorKind = fileError?.error instanceof ApiError ? fileError.error.kind : undefined;

  useEffect(() => { if (preview) previewElement.current?.scrollIntoView?.({ block: 'nearest' }); }, [preview]);

  function revokePreview() {
    if (imageURL.current) URL.revokeObjectURL(imageURL.current);
    imageURL.current = null;
  }

  function cancelTransfer() {
    pending.current?.abort(); pending.current = null; setBusy(undefined);
  }

  async function loadFile(file: OperationArtifactFile, mode: FileMode) {
    if (pending.current) return;
    const controller = new AbortController();
    pending.current = controller;
    setBusy({ file, mode }); setFileError(undefined);
    if (mode === 'preview') { revokePreview(); setPreview(undefined); }
    try {
      const blob = await getArtifactFile(url, credentials, artifactID, file.id, controller.signal, mode === 'preview' ? PREVIEW_BYTES : MAX_FILE_BYTES);
      if (controller.signal.aborted || pending.current !== controller) return;
      if (mode === 'download') download(blob, file.name);
      else if (previewKind(file) === 'image') {
        const type = file.media_type.split(';')[0].trim().toLowerCase();
        const bytes = new Uint8Array(await blob.arrayBuffer());
        if (controller.signal.aborted || pending.current !== controller) return;
        if (!matchesRaster(bytes, type)) { setFileError({ file, mode, error: 'invalidPreview' }); return; }
        imageURL.current = URL.createObjectURL(blob.slice(0, blob.size, type));
        setPreview({ file, kind: 'image', url: imageURL.current });
      } else {
        const text = await blob.text();
        if (!controller.signal.aborted && pending.current === controller) setPreview({ file, kind: 'text', text });
      }
    } catch (cause) {
      if (!controller.signal.aborted && pending.current === controller) setFileError({ file, mode, error: cause instanceof ApiError || cause instanceof FileTooLargeError ? cause : new ApiError('unavailable') });
    } finally {
      if (!controller.signal.aborted && pending.current === controller) { pending.current = null; setBusy(undefined); }
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    cancelTransfer(); revokePreview(); setPreview(undefined); setFileError(undefined);
    const identity = { url, credentials, id: artifactID, revision };
    setSource(identity);
    void getArtifact(url, credentials, artifactID, controller.signal).then(artifact => {
      if (controller.signal.aborted) return;
      setSource({ ...identity, artifact });
      const file = initialFileID ? artifact.files?.find(item => item.id === initialFileID) : undefined;
      if (file && file.size <= PREVIEW_BYTES && previewKind(file)) void loadFile(file, 'preview');
    }).catch(cause => {
      if (!controller.signal.aborted) setSource({ ...identity, error: cause instanceof ApiError ? cause : new ApiError('unavailable') });
    });
    return () => { controller.abort(); pending.current?.abort(); pending.current = null; revokePreview(); };
  }, [url, credentials, artifactID, initialFileID, revision]);

  function close() { cancelTransfer(); revokePreview(); onClose(); }
  const artifact = current?.artifact;
  return <Dialog open className="artifact-dialog" title={t($ => $.artifacts.title)} onClose={close}>
    {!current || !artifact && !current.error ? <p className="artifact-loading" role="status"><LoaderCircle size={17} className="connection-form-spinner" aria-hidden="true" />{t($ => $.artifacts.loading)}</p>
      : errorKind ? <div className="notice" role="alert"><p>{t($ => $.artifacts.errors[errorKind])}</p><Button variant="secondary" onClick={() => setRevision(value => value + 1)}>{t($ => $.artifacts.retry)}</Button></div>
        : artifact && <div className="artifact-content"><h3>{artifact.name}</h3>
          <dl className="detail-list">
            <div><dt>{t($ => $.operations.artifactId)}</dt><dd><code>{artifact.id}</code></dd></div>
            <div><dt>{t($ => $.operations.action)}</dt><dd>{artifact.action_name}</dd></div>
            <div><dt>{t($ => $.operations.agentId)}</dt><dd><code>{artifact.agent_id}</code></dd></div>
            {artifact.command_id && <div><dt>{t($ => $.operations.commandId)}</dt><dd><code>{artifact.command_id}</code></dd></div>}
            <div><dt>{t($ => $.operations.createdAt)}</dt><dd><time dateTime={artifact.created_at}>{new Date(artifact.created_at).toLocaleString()}</time></dd></div>
          </dl>
          <section><h3>{t($ => $.operations.files)}</h3><p className="form-hint">{t($ => $.artifacts.previewLimit)}</p>
            {initialFileID && !artifact.files?.some(file => file.id === initialFileID) && <p className="notice">{t($ => $.artifacts.fileMissing)}</p>}
            {artifact.files?.length ? <ul className="artifact-files">{artifact.files.map(file => <li key={file.id}><article className="artifact-file" aria-label={file.name}>
              <h4>{file.name}</h4><p className="form-hint">{file.media_type} · {t($ => $.operations.bytes, { size: file.size.toLocaleString() })}</p>
              <details className="artifact-file-metadata"><summary>{t($ => $.artifacts.metadata)}</summary><dl className="detail-list">
                <div><dt>{t($ => $.operations.fileId)}</dt><dd><code>{file.id}</code></dd></div>
                <div><dt>{t($ => $.operations.sha256)}</dt><dd><code>{file.sha256}</code></dd></div>
              </dl></details>
              <div className="artifact-file-controls">
                {file.size <= PREVIEW_BYTES && previewKind(file) && <Button variant="secondary" disabled={Boolean(busy)} aria-label={t($ => $.artifacts.previewFile, { name: file.name })} onClick={() => void loadFile(file, 'preview')}>{t($ => $.artifacts.preview)}</Button>}
                <Button variant="secondary" disabled={Boolean(busy) || file.size > MAX_FILE_BYTES} aria-label={t($ => $.artifacts.downloadFile, { name: file.name })} onClick={() => void loadFile(file, 'download')}><Download size={15} aria-hidden="true" />{t($ => $.artifacts.download)}</Button>
              </div>
              {file.size > MAX_FILE_BYTES ? <p className="form-hint">{t($ => $.artifacts.downloadLimit)}</p> : (!previewKind(file) || file.size > PREVIEW_BYTES) && <p className="form-hint">{t($ => $.artifacts.noPreview)}</p>}
            </article></li>)}</ul> : <p className="form-hint">{t($ => $.artifacts.noFiles)}</p>}
          </section>
          {busy && <div className="artifact-transfer" role="status"><p><LoaderCircle size={17} className="connection-form-spinner" aria-hidden="true" />{t($ => $.artifacts.loadingFile, { name: busy.file.name })}</p><Button variant="secondary" onClick={cancelTransfer}>{t($ => $.artifacts.cancelTransfer)}</Button></div>}
          {fileError && <div className="notice" role="alert"><p>{fileError.error === 'invalidPreview' ? t($ => $.artifacts.invalidPreview) : fileError.error instanceof FileTooLargeError ? t($ => $.artifacts.fileTooLarge) : fileErrorKind && t($ => $.artifacts.errors[fileErrorKind])}</p>
            <Button variant="secondary" disabled={Boolean(busy)} onClick={() => void loadFile(fileError.file, fileError.mode)}>{t($ => $.artifacts.retry)}</Button>
          </div>}
          {preview && <section className="artifact-preview" ref={previewElement} aria-label={t($ => $.artifacts.filePreview)}><h3>{preview.file.name}</h3>
            {preview.kind === 'image' ? <img src={preview.url} alt={preview.file.name} onError={() => {
              revokePreview(); setPreview(undefined); setFileError({ file: preview.file, mode: 'preview', error: 'invalidPreview' });
            }} /> : <pre tabIndex={0}>{preview.text}</pre>}
          </section>}
        </div>}
    <div className="dialog-actions"><Button variant="secondary" onClick={close}>{t($ => $.artifacts.close)}</Button></div>
  </Dialog>;
}

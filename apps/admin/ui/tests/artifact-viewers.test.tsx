import { Blob as NodeBlob } from 'node:buffer';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { OperationArtifact } from '../src/api/operation-types';
import { ArtifactDialog } from '../src/components/ArtifactDialog';
import { ReportDialog } from '../src/components/ReportDialog';

const url = 'https://commander.example.test';
const credentials = { username: 'operator', password: 'secret' };
const artifact: OperationArtifact = { id: 'artifact-1', name: 'Page evidence', agent_id: 'agent-one', action_name: 'capture-page', created_at: '2026-10-06T10:00:00Z', files: [
  { id: 'text', name: 'page.html', media_type: 'text/html', size: 100, sha256: 'a'.repeat(64) },
  { id: 'image', name: 'page.png', media_type: 'image/png', size: 50, sha256: 'b'.repeat(64) },
] };

function browserFiles() {
  const blobs: Blob[] = [];
  const createObjectURL = vi.fn((blob: Blob) => { blobs.push(blob); return `blob:fixture-${blobs.length}`; });
  const revokeObjectURL = vi.fn();
  vi.stubGlobal('Blob', NodeBlob);
  vi.stubGlobal('URL', class extends URL {
    static createObjectURL = createObjectURL;
    static revokeObjectURL = revokeObjectURL;
  });
  const downloads: { href: string; name: string }[] = [];
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { downloads.push({ href: this.href, name: this.download }); });
  return { blobs, downloads, createObjectURL, revokeObjectURL };
}

function artifactAPI(fileReply?: (fileID: string, options: RequestInit) => Response | Promise<Response>) {
  const fetch = vi.fn(async (input: RequestInfo | URL, options: RequestInit = {}) => {
    const address = String(input);
    if (address.includes('/files/')) return fileReply!(address.split('/').at(-1)!, options);
    return Response.json(artifact);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

describe('artifact and report viewers', () => {
  it('previews HTML as text and downloads original bytes without rendering active content', async () => {
    const files = browserFiles();
    const html = '<script>window.untrusted = true</script><h1>Saved page</h1>';
    const fetch = artifactAPI(() => new Response(html, { headers: { 'Content-Type': 'text/html' } }));
    const view = render(<ArtifactDialog url={url} credentials={credentials} artifactID={artifact.id} initialFileID="text" onClose={vi.fn()} />);

    expect(await screen.findByText(html)).toBeVisible();
    expect(screen.queryByRole('heading', { name: 'Saved page' })).not.toBeInTheDocument();
    expect(view.container.querySelector('script, iframe, object, embed')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Download page.html' }));
    await waitFor(() => expect(files.downloads).toHaveLength(1));

    expect(files.downloads[0]).toEqual({ href: expect.stringMatching(/^blob:/), name: 'page.html' });
    expect(await files.blobs.at(-1)!.text()).toBe(html);
    expect(fetch.mock.calls.every(([address, options]) => String(address).startsWith(url) && options?.method === 'GET')).toBe(true);
    view.unmount();
    expect(files.revokeObjectURL).toHaveBeenCalled();
  });

  it('releases an image preview when closed and constrains its media type to the declared raster type', async () => {
    const files = browserFiles();
    const png = Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jB3sAAAAASUVORK5CYII='), character => character.charCodeAt(0));
    artifactAPI(() => new Response(png, { headers: { 'Content-Type': 'image/svg+xml' } }));
    const view = render(<ArtifactDialog url={url} credentials={credentials} artifactID={artifact.id} initialFileID="image" onClose={vi.fn()} />);

    expect(await screen.findByRole('img', { name: 'page.png' })).toHaveAttribute('src', 'blob:fixture-1');
    expect(files.blobs[0].type).toBe('image/png');
    view.unmount();

    expect(files.revokeObjectURL).toHaveBeenCalledWith('blob:fixture-1');
  });

  it('does not fetch unsupported or oversized previews automatically', async () => {
    browserFiles();
    const files = [
      { ...artifact.files![0], id: 'large', name: 'large.txt', media_type: 'text/plain', size: 3 * 1024 * 1024 },
      { ...artifact.files![1], id: 'pdf', name: 'page.pdf', media_type: 'application/pdf' },
    ];
    const fetch = vi.fn(async () => Response.json({ ...artifact, files }));
    vi.stubGlobal('fetch', fetch);
    const view = render(<ArtifactDialog url={url} credentials={credentials} artifactID={artifact.id} initialFileID="large" onClose={vi.fn()} />);
    await screen.findByText('large.txt');
    view.unmount();
    render(<ArtifactDialog url={url} credentials={credentials} artifactID={artifact.id} initialFileID="pdf" onClose={vi.fn()} />);
    await screen.findByText('page.pdf');

    expect(fetch).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
  });

  it('aborts file loading on unmount and ignores a late download response', async () => {
    const files = browserFiles();
    let finish!: (response: Response) => void;
    let signal: AbortSignal | null | undefined;
    artifactAPI((_id, options) => { signal = options.signal; return new Promise<Response>(resolve => { finish = resolve; }); });
    const view = render(<ArtifactDialog url={url} credentials={credentials} artifactID={artifact.id} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Download page.html' }));
    view.unmount();
    await act(async () => finish(new Response('late file')));

    expect(signal?.aborted).toBe(true);
    expect(files.downloads).toHaveLength(0);
    expect(files.createObjectURL).not.toHaveBeenCalled();
  });

  it('shows a sanitized missing-file error without starting a download', async () => {
    const files = browserFiles();
    artifactAPI(() => Response.json({ private: 'storage credentials' }, { status: 404 }));
    render(<ArtifactDialog url={url} credentials={credentials} artifactID={artifact.id} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Download page.html' }));

    expect(await screen.findByRole('alert')).not.toHaveTextContent('storage credentials');
    expect(files.downloads).toHaveLength(0);
  });

  it('downloads the saved report as JSON and can retry a failed report read', async () => {
    const files = browserFiles();
    const report = { id: 'report-1', operation_id: 'operation-1', mission: 'review-website', output: { findings: ['Missing page title'] }, created_at: artifact.created_at };
    let unavailable = true;
    vi.stubGlobal('fetch', vi.fn(async () => unavailable ? new Response(null, { status: 503 }) : Response.json(report)));
    render(<ReportDialog url={url} credentials={credentials} operationID={report.operation_id} onClose={vi.fn()} />);
    expect(await screen.findByRole('alert')).toBeVisible();
    unavailable = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText(/Missing page title/)).toBeVisible();
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Download JSON' }));

    expect(files.downloads).toHaveLength(1);
    expect(files.downloads[0].name).toMatch(/report-1.*\.json$/);
    expect(JSON.parse(await files.blobs[0].text())).toEqual(report);
  });
});

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Operation } from '../src/api/operation-types';
import { OperationsPage } from '../src/pages/OperationsPage';
import { upstreamURL } from './fakes/proxy';
import { artifact, connection, credentials, fixture, openOperation, operation, report } from './fixtures/operation-resources';

describe('operation evidence', () => {
  it('shows a late artifact after refreshing a completed operation with an interrupted task', async () => {
    let snapshot: Operation = { ...operation,
      tasks: [{ ...operation.tasks![0], status: 'interrupted', result: undefined, error: 'Execution outcome is unknown' }],
      progress: { ...operation.progress, task_counts: { interrupted: 1 }, finished_tasks: 0, settled: false } };
    const fetch = fixture(url => {
      if (url.endsWith('/operations')) return Response.json({ items: [snapshot] });
      if (url.endsWith(`/operations/${operation.id}`)) return Response.json(snapshot);
    });
    render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    await openOperation();

    expect(screen.getByText('Completed', { selector: '.catalog-status' })).toBeVisible();
    expect(screen.getByText('0/1 tasks finished')).toBeVisible();
    expect(screen.getByText('No artifacts saved in task results.')).toBeVisible();
    expect(screen.getByText(operation.report_id!)).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Cancel operation' })).not.toBeInTheDocument();

    snapshot = operation;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));

    expect(await screen.findByText(artifact.name, { selector: 'summary span span' })).toBeVisible();
    expect(screen.getByRole('heading', { name: operation.id })).toBeVisible();
    expect(screen.getByText('Completed', { selector: '.catalog-status' })).toBeVisible();
    expect(screen.getByText('1/1 tasks finished')).toBeVisible();
    expect(screen.queryByText('No artifacts saved in task results.')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel operation' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'View report' }));
    expect(await within(screen.getByRole('dialog')).findByText(/The reviewed pages load correctly/)).toBeVisible();
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Close dialog' }));
    fireEvent.click(screen.getByText(artifact.name, { selector: 'summary span span' }).closest('summary')!);
    fireEvent.click(screen.getByRole('button', { name: 'View artifact' }));
    const dialog = screen.getByRole('dialog');
    expect(await within(dialog).findByText('desktop.png')).toBeVisible();
    expect(within(dialog).getByText(artifact.id)).toBeVisible();
    expect(within(dialog).getByRole('button', { name: 'Download desktop.png' })).toBeVisible();
    expect(fetch.mock.calls.every(([, options]) => options?.method === 'GET')).toBe(true);
    expect(fetch.mock.calls.some(([url]) => upstreamURL(url).includes('/files/'))).toBe(false);
  });

  it('opens saved reports and artifacts on demand from details and the graph using only queries', async () => {
    const fetch = fixture();
    render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    await openOperation();
    expect(fetch.mock.calls.some(([url]) => upstreamURL(url).endsWith('/report') || upstreamURL(url).includes('/artifacts/'))).toBe(false);

    fireEvent.click(screen.getByRole('button', { name: 'View report' }));
    const reportDialog = screen.getByRole('dialog');
    expect(await within(reportDialog).findByText(/The reviewed pages load correctly/)).toBeVisible();
    fireEvent.click(within(reportDialog).getByRole('button', { name: 'Close dialog' }));
    fireEvent.click(screen.getByText(artifact.name, { selector: 'summary span span' }).closest('summary')!);
    fireEvent.click(screen.getByRole('button', { name: 'View artifact' }));
    const artifactDialog = screen.getByRole('dialog');
    expect(await within(artifactDialog).findByText('desktop.png')).toBeVisible();
    fireEvent.click(within(artifactDialog).getByRole('button', { name: 'Close dialog' }));
    fireEvent.click(screen.getByRole('button', { name: 'Execution graph' }));
    fireEvent.click(await screen.findByRole('button', { name: `Inspect Artifact ${artifact.name} · artifact:${artifact.id}` }));
    fireEvent.click(within(screen.getByRole('region', { name: 'Node details' })).getByRole('button', { name: 'View artifact' }));
    expect(await within(screen.getByRole('dialog')).findByText('desktop.png')).toBeVisible();
    expect(fetch.mock.calls.every(([, options]) => options?.method === 'GET')).toBe(true);
    expect(fetch.mock.calls.some(([url]) => upstreamURL(url).includes('/files/'))).toBe(false);
  });

  it('closes evidence and aborts its pending request when credentials are forgotten', async () => {
    let finish!: (response: Response) => void;
    let signal: AbortSignal | null | undefined;
    fixture((url, options) => {
      if (url.endsWith('/report')) {
        signal = options.signal;
        return new Promise<Response>(resolve => { finish = resolve; });
      }
    });
    const view = render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    await openOperation();
    fireEvent.click(screen.getByRole('button', { name: 'View report' }));

    view.rerender(<OperationsPage connection={connection} onSignIn={vi.fn()} />);
    await act(async () => finish(Response.json(report)));

    expect(signal?.aborted).toBe(true);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByText(/The reviewed pages load correctly/)).not.toBeInTheDocument();
  });
});

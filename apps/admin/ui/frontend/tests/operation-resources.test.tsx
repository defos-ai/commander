import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Operation, OperationArtifact } from '../src/api/operation-types';
import { OperationsPage } from '../src/pages/OperationsPage';

const connection = { id: 'local', name: 'Commander', url: 'https://commander.example.test' };
const credentials = { username: 'operator', password: 'secret' };
const now = '2026-10-06T10:00:00Z';
const artifact: OperationArtifact = { id: 'artifact-page', name: 'Homepage evidence', agent_id: 'agent-one', action_name: 'capture-page', created_at: now,
  files: [{ id: 'file-page', name: 'desktop.png', media_type: 'image/png', size: 1024, sha256: 'a'.repeat(64) }] };
const operation: Operation = { id: 'operation-review', status: 'completed', report_id: 'report-review', input: {},
  mission: { name: 'review-website', objective: 'Review website quality.', required_actions: [], starting_actions: [], input_schema: {}, output_schema: {} },
  tasks: [{ id: 'task-capture', operation_id: 'operation-review', agent_id: 'agent-one', action_name: 'capture-page', status: 'succeeded', started_at: now,
    result: { action_name: 'capture-page', output: {}, artifacts: [artifact] } }], questions: [], events: [], created_at: now, updated_at: now };
const report = { id: 'report-review', operation_id: operation.id, mission: operation.mission.name, output: { summary: 'The reviewed pages load correctly.' }, created_at: now };

function fixture(respond?: (url: string, options: RequestInit) => Response | Promise<Response> | undefined) {
  const fetch = vi.fn(async (input: RequestInfo | URL, options: RequestInit = {}) => {
    const url = String(input);
    const override = respond?.(url, options);
    if (override) return override;
    if (url.endsWith('/operations')) return Response.json({ items: [operation] });
    if (url.endsWith('/report')) return Response.json(report);
    if (url.endsWith('/artifacts/artifact-page')) return Response.json(artifact);
    if (url.endsWith('/graph')) return Response.json({ operation_id: operation.id, status: operation.status, updated_at: now,
      nodes: [{ id: `artifact:${artifact.id}`, kind: 'artifact', artifact }], edges: [] });
    return Response.json(operation);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

async function openOperation() {
  fireEvent.click(await screen.findByRole('button', { name: `View operation ${operation.id}` }));
  await screen.findByText(operation.mission.objective);
}

describe('operation evidence', () => {
  it('opens saved reports and artifacts on demand from details and the graph using only queries', async () => {
    const fetch = fixture();
    render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    await openOperation();
    expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/report') || String(url).includes('/artifacts/'))).toBe(false);

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
    expect(fetch.mock.calls.some(([url]) => String(url).includes('/files/'))).toBe(false);
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

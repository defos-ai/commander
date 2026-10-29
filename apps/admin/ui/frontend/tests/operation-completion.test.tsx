import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { OperationsPage } from '../src/pages/OperationsPage';
import { upstreamURL } from './fakes/proxy';
import { completed, connection, credentials, detail, fixture, operation, proposalDocument, proposedOperation, task, view } from './fixtures/operation-completion';

const confirmation = 'I have reviewed the final output and approve completion.';

describe('operation completion', () => {
  it('loads a saved proposal losslessly and requires explicit approval before completion', async () => {
    const api = fixture((_url, body) => {
      api.change(state => { state.current = completed(state.current, body.output); });
      return Response.json(api.snapshot().current);
    });
    view();
    await screen.findByRole('button', { name: `View operation ${operation.id}` });
    const output = String.raw`{"summary":"Prepared","evidence_id":9007199254740993,"path":"\u0061\/b"}`;
    api.fetch.mockResolvedValueOnce(new Response(proposalDocument(output)));

    await detail();
    fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));

    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByLabelText('Final output (JSON)')).toHaveValue(output);
    const metadata = within(dialog).getByRole('heading', { name: 'Proposed final output' }).closest('section')!;
    expect(metadata).toHaveTextContent(/Proposed by.*Model/);
    expect(within(metadata).getByText(/Saving a proposal does not complete/)).toBeVisible();
    expect(within(dialog).getByRole('button', { name: 'Complete operation' })).toBeDisabled();
    expect(api.posts()).toHaveLength(0);

    fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Complete operation' }));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(api.posts()).toHaveLength(1);
    expect(api.posts()[0][1]!.body).toBe(`{"output":${output}}`);
  });

  it('keeps an edited draft while polling and loads a newer proposal only by operator choice', async () => {
    vi.useFakeTimers();
    try {
      const api = fixture();
      api.change(state => { state.current = proposedOperation({ summary: 'First proposal' }); });
      await act(async () => { view(); });
      fireEvent.click(screen.getByRole('button', { name: `View operation ${operation.id}` }));
      await act(async () => {});
      fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));
      const dialog = screen.getByRole('dialog');
      const input = within(dialog).getByLabelText('Final output (JSON)');
      expect(input).toHaveValue('{"summary":"First proposal"}');
      fireEvent.change(input, { target: { value: '{"summary":"My edited final report"}' } });
      fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));

      await act(async () => { await vi.advanceTimersByTimeAsync(10_001); });

      expect(input).toHaveValue('{"summary":"My edited final report"}');
      expect(within(dialog).getByRole('checkbox', { name: confirmation })).toBeChecked();
      api.change(state => {
        state.current.completion_proposal = { ...state.current.completion_proposal!,
          id: '22222222-2222-4222-8222-222222222222', output: { summary: 'New evidence reviewed' } };
        state.current.updated_at = '2026-10-05T12:01:00Z';
      });

      await act(async () => { await vi.advanceTimersByTimeAsync(10_001); });

      expect(input).toHaveValue('{"summary":"My edited final report"}');
      expect(within(dialog).getByRole('checkbox', { name: confirmation })).not.toBeChecked();
      expect(within(dialog).getByText('A newer proposal is available. Your current draft is kept until you load it.')).toBeVisible();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Load latest proposal' }));

      expect(input).toHaveValue('{"summary":"New evidence reviewed"}');
      expect(within(dialog).getByRole('checkbox', { name: confirmation })).not.toBeChecked();
      expect(within(dialog).getByRole('button', { name: 'Complete operation' })).toBeDisabled();
      expect(api.posts()).toHaveLength(0);
    } finally { vi.useRealTimers(); }
  });

  it('reviews the saved mission, requires explicit approval and submits the final output unchanged', async () => {
    const api = fixture((_url, body) => {
      api.change(state => { state.current = completed(state.current, body.output); });
      return Response.json(api.snapshot().current);
    });
    view(); await detail();

    fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));

    const dialog = screen.getByRole('dialog', { name: 'Complete operation' });
    for (const objective of operation.mission.objectives) expect(within(dialog).getByText(objective)).toBeVisible();
    expect(within(dialog).getByText('Mission output schema')).toBeVisible();
    const submit = within(dialog).getByRole('button', { name: 'Complete operation' });
    expect(submit).toBeDisabled();
    const output = String.raw`{"summary":"Reviewed","evidence_id":9007199254740993,"path":"\u0061\/b"}`;
    fireEvent.change(within(dialog).getByLabelText('Final output (JSON)'), { target: { value: output } });
    fireEvent.submit(dialog.querySelector('form')!);
    expect(api.posts()).toHaveLength(0);

    fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
    fireEvent.click(submit);

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(api.posts()).toHaveLength(1);
    expect(upstreamURL(api.posts()[0][0])).toBe(`${connection.url}/api/v1/operations/${operation.id}/complete`);
    expect(api.posts()[0][1]!.body).toBe(`{"output":${output}}`);
    expect(await screen.findByText('Completed', { selector: '.catalog-status' })).toBeVisible();
    expect(screen.getByText('Completed by').closest('div')).toHaveTextContent('Operator');
    expect(screen.queryByRole('button', { name: 'Complete operation' })).not.toBeInTheDocument();
  });

  it('keeps invalid JSON local and permits explicit correction after a rejected output schema', async () => {
    const api = fixture((_url, body) => {
      if (!(body.output as Record<string, unknown>).summary) return Response.json({ error: 'invalid_output' }, { status: 422 });
      api.change(state => { state.current = completed(state.current, body.output); });
      return Response.json(api.snapshot().current);
    });
    view(); await detail();
    fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));
    const dialog = screen.getByRole('dialog');
    const output = within(dialog).getByLabelText('Final output (JSON)');
    fireEvent.change(output, { target: { value: '{' } });
    fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
    fireEvent.submit(dialog.querySelector('form')!);

    expect(within(dialog).getByRole('alert')).toHaveTextContent('valid JSON');
    expect(api.posts()).toHaveLength(0);
    fireEvent.change(output, { target: { value: '{}' } });
    fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Complete operation' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('output');
    expect(output).toHaveValue('{}');
    expect(api.posts()).toHaveLength(1);
    fireEvent.change(output, { target: { value: '{"summary":"Reviewed after correction"}' } });
    const approve = within(dialog).getByRole('checkbox', { name: confirmation });
    if (!(approve as HTMLInputElement).checked) fireEvent.click(approve);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Complete operation' }));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(api.posts()).toHaveLength(2);
  });

  it.each(['queued', 'running', 'pending question', 'automatic continuation'])('blocks completion while %s work remains', async blocker => {
    const api = fixture();
    api.change(state => {
      if (blocker === 'pending question') {
        state.current.questions = [{ id: 'question-1', status: 'pending', text: 'Continue?', asked_at: operation.created_at,
          fallback_at: '2099-01-01T00:00:00Z' }];
        state.current.progress.pending_questions = 1;
      } else {
        state.current.tasks = [task(blocker === 'automatic continuation' ? 'succeeded' : blocker)];
        if (blocker === 'automatic continuation') state.current.tasks[0].continuation = {
          version: 1, status: 'pending', decision_by: 'code', proposed_actions: [], created_task_ids: [],
        };
      }
    });
    view(); await detail();
    fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Final output (JSON)'), { target: { value: '{"summary":"Reviewed"}' } });
    fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));

    expect(within(dialog).getByRole('button', { name: 'Complete operation' })).toBeDisabled();
    fireEvent.submit(dialog.querySelector('form')!);
    expect(api.posts()).toHaveLength(0);
  });

  it('allows an interrupted outcome with a limitation warning and leaves its task unfinished', async () => {
    const api = fixture((_url, body) => {
      api.change(state => { state.current = completed(state.current, body.output); });
      return Response.json(api.snapshot().current);
    });
    api.change(state => {
      state.current.tasks = [{ ...task('interrupted'), continuation: {
        version: 1, status: 'pending', decision_by: 'model', proposed_actions: [], created_task_ids: [],
      } }];
      state.current.progress = { ...state.current.progress, total_tasks: 1, finished_tasks: 0, task_counts: { interrupted: 1 }, pending_decisions: 1 };
    });
    view(); await detail();
    fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));
    const dialog = screen.getByRole('dialog');

    expect(dialog).toHaveTextContent(/interrupt|unknown/i);
    expect(dialog).toHaveTextContent(/limitation/i);
    fireEvent.change(within(dialog).getByLabelText('Final output (JSON)'), { target: { value: '{"summary":"Build outcome remains unknown"}' } });
    fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Complete operation' }));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(api.posts()).toHaveLength(1);
    expect(api.snapshot().current.progress.finished_tasks).toBe(0);
    expect(api.snapshot().current.progress.settled).toBe(false);
  });

  it.each([409, 503])('requires saved-state readback after HTTP %s, including after dismissing the dialog', async status => {
    const api = fixture(() => Response.json({ error: status === 409 ? 'operation_conflict' : 'operation_write_uncertain' }, { status }));
    view(); await detail();
    fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Final output (JSON)'), { target: { value: '{"summary":"Reviewed"}' } });
    fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Complete operation' }));

    expect(await within(dialog).findByRole('alert')).toBeVisible();
    fireEvent.submit(dialog.querySelector('form')!);
    expect(api.posts()).toHaveLength(1);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));

    expect(screen.getByText('Completion was submitted. View the operation before trying again.')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Complete operation' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.posts()).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'View operation' }));

    await waitFor(() => expect(screen.queryByText('Completion was submitted. View the operation before trying again.')).not.toBeInTheDocument());
    expect(await screen.findByRole('button', { name: 'Complete operation' })).toBeEnabled();
    expect(api.posts()).toHaveLength(1);
  });

  it('retains the output draft while polling adds a completion blocker', async () => {
    vi.useFakeTimers();
    try {
      const api = fixture();
      await act(async () => { view(); });
      fireEvent.click(screen.getByRole('button', { name: `View operation ${operation.id}` }));
      await act(async () => {});
      fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));
      const dialog = screen.getByRole('dialog');
      fireEvent.change(within(dialog).getByLabelText('Final output (JSON)'), { target: { value: '{"summary":"My draft"}' } });
      fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
      api.change(state => { state.current.tasks = [task('running')]; state.current.updated_at = '2026-10-05T10:01:00Z'; });

      await act(async () => { await vi.advanceTimersByTimeAsync(10_001); });

      expect(within(dialog).getByLabelText('Final output (JSON)')).toHaveValue('{"summary":"My draft"}');
      expect(within(dialog).getByRole('checkbox', { name: confirmation })).not.toBeChecked();
      expect(within(dialog).getByRole('button', { name: 'Complete operation' })).toBeDisabled();
      fireEvent.submit(dialog.querySelector('form')!);
      expect(api.posts()).toHaveLength(0);

      api.change(state => { state.current.tasks = []; state.current.updated_at = '2026-10-05T10:02:00Z'; });

      await act(async () => { await vi.advanceTimersByTimeAsync(10_001); });

      expect(within(dialog).getByLabelText('Final output (JSON)')).toHaveValue('{"summary":"My draft"}');
      expect(within(dialog).getByRole('button', { name: 'Complete operation' })).toBeDisabled();
      fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
      expect(within(dialog).getByRole('button', { name: 'Complete operation' })).toBeEnabled();
      expect(api.posts()).toHaveLength(0);
    } finally { vi.useRealTimers(); }
  });

  it('keeps an in-flight completion alive when polling observes its terminal result', async () => {
    vi.useFakeTimers();
    try {
      let finish!: (response: Response) => void;
      let signal: AbortSignal | null | undefined;
      const api = fixture((_url, body, requestSignal) => {
        signal = requestSignal;
        api.change(state => { state.current = completed(state.current, body.output); });
        return new Promise<Response>(resolve => { finish = resolve; });
      });
      await act(async () => { view(); });
      fireEvent.click(screen.getByRole('button', { name: `View operation ${operation.id}` }));
      await act(async () => {});
      fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));
      const dialog = screen.getByRole('dialog');
      fireEvent.change(within(dialog).getByLabelText('Final output (JSON)'), { target: { value: '{"summary":"Reviewed"}' } });
      fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
      fireEvent.click(within(dialog).getByRole('button', { name: 'Complete operation' }));
      fireEvent.submit(dialog.querySelector('form')!);

      await act(async () => { await vi.advanceTimersByTimeAsync(10_001); });

      expect(signal?.aborted).toBe(false);
      expect(api.posts()).toHaveLength(1);
      await act(async () => { finish(Response.json(api.snapshot().current)); });
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(screen.getByText('Completed by').closest('div')).toHaveTextContent('Operator');
    } finally { vi.useRealTimers(); }
  });

  it('aborts a submitted completion and suppresses its late response after sign-out', async () => {
    let finish!: (response: Response) => void;
    let signal: AbortSignal | null | undefined;
    const api = fixture((_url, _body, requestSignal) => {
      signal = requestSignal;
      return new Promise<Response>(resolve => { finish = resolve; });
    });
    const props = { connection, credentials, onSignIn: vi.fn(), onSelect: vi.fn(), selectedID: operation.id };
    const rendered = render(<OperationsPage {...props} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Complete operation' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Final output (JSON)'), { target: { value: '{"summary":"Reviewed"}' } });
    fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Complete operation' }));
    expect(signal?.aborted).toBe(false);

    rendered.rerender(<OperationsPage {...props} credentials={undefined} />);
    await act(async () => { finish(Response.json(completed(operation, { summary: 'Late reply' }))); });

    expect(signal?.aborted).toBe(true);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByText('Completed by')).not.toBeInTheDocument();
    expect(api.posts()).toHaveLength(1);

    rendered.rerender(<OperationsPage {...props} />);

    expect(await screen.findByRole('button', { name: 'Complete operation' })).toBeDisabled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('keeps a dismissed in-flight submission blocked through polling until an explicit readback', async () => {
    vi.useFakeTimers();
    try {
      let finish!: (response: Response) => void;
      let signal: AbortSignal | null | undefined;
      const api = fixture((_url, _body, requestSignal) => {
        signal = requestSignal;
        return new Promise<Response>(resolve => { finish = resolve; });
      });
      await act(async () => { view(); });
      fireEvent.click(screen.getByRole('button', { name: `View operation ${operation.id}` }));
      await act(async () => {});
      fireEvent.click(screen.getByRole('button', { name: 'Complete operation' }));
      const dialog = screen.getByRole('dialog');
      fireEvent.change(within(dialog).getByLabelText('Final output (JSON)'), { target: { value: '{"summary":"Reviewed"}' } });
      fireEvent.click(within(dialog).getByRole('checkbox', { name: confirmation }));
      fireEvent.click(within(dialog).getByRole('button', { name: 'Complete operation' }));

      fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
      await act(async () => { await vi.advanceTimersByTimeAsync(10_001); finish(Response.json(completed(operation, {}))); });

      expect(signal?.aborted).toBe(true);
      expect(screen.getByRole('button', { name: 'Complete operation' })).toBeDisabled();
      expect(screen.getByText('Completion was submitted. View the operation before trying again.')).toBeVisible();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(api.posts()).toHaveLength(1);

      await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'View operation' })); });

      expect(screen.getByRole('button', { name: 'Complete operation' })).toBeEnabled();
      expect(api.posts()).toHaveLength(1);
    } finally { vi.useRealTimers(); }
  });
});

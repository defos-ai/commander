import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Operation } from '../src/api/operation-types';
import { OperationsPage } from '../src/pages/OperationsPage';

const connection = { id: 'test', name: 'Commander', url: 'https://commander.example.test' };
const credentials = { username: 'operator', password: 'secret' };
const now = '2026-10-05T10:00:00Z';
const mission = { name: 'review-website', objective: 'Review website quality.', required_actions: [], starting_actions: [],
  input_schema: { type: 'object', required: ['url'], properties: { url: { type: 'string' } } }, output_schema: {} };
const operation: Operation = { id: 'operation-review', mission, input: { url: 'https://defos.ai' }, status: 'waiting', tasks: [], events: [],
  questions: [{ id: 'question-browser', status: 'pending', text: 'How should the review continue?', options: ['Configure the browser', 'Use HTTP checks'],
    asked_at: now, expires_at: '2099-01-01T00:00:00Z' }], created_at: now, updated_at: now };

function fixture(post?: (url: string, body: Record<string, unknown>, signal?: AbortSignal | null) => Response | Promise<Response>) {
  const state = { items: [operation], current: structuredClone(operation) };
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = String(input);
    if (init.method === 'POST') return post!(url, JSON.parse(init.body as string) as Record<string, unknown>, init.signal);
    if (url.endsWith('/missions')) return Response.json({ items: [mission] });
    if (url.endsWith('/operations')) return Response.json({ items: state.items });
    return Response.json(state.current);
  });
  vi.stubGlobal('fetch', fetch);
  return { state, fetch, posts: () => fetch.mock.calls.filter(([, init]) => init?.method === 'POST') };
}

function view() { return render(<OperationsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />); }
async function detail() { fireEvent.click(await screen.findByRole('button', { name: `View operation ${operation.id}` })); await screen.findByText(mission.objective); }
async function startDialog() {
  fireEvent.click(await screen.findByRole('button', { name: 'Start operation' }));
  const dialog = screen.getByRole('dialog', { name: 'Start operation' });
  await within(dialog).findByLabelText('Mission input (JSON)');
  return dialog;
}

describe('operation controls', () => {
  it('validates input, sends a stable start ID, and opens the saved operation without assigning agents', async () => {
    let release!: () => void;
    const held = new Promise<void>(resolve => { release = resolve; });
    const api = fixture(async (_url, body) => {
      if (!(body.input as Record<string, unknown>).url) return Response.json({ error: 'invalid_input' }, { status: 422 });
      await held;
      api.state.current = { ...operation, id: body.operation_id as string, status: 'planning', questions: [] };
      return Response.json(api.state.current, { status: 201 });
    });
    view();
    const dialog = await startDialog();
    const input = within(dialog).getByLabelText('Mission input (JSON)');
    fireEvent.change(input, { target: { value: '{' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Start operation' }));
    expect(within(dialog).getByRole('alert')).toHaveTextContent('Enter valid JSON');
    expect(api.posts()).toHaveLength(0);

    fireEvent.change(input, { target: { value: '{}' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Start operation' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('input does not match the mission schema');
    const first = JSON.parse(api.posts()[0][1]!.body as string);
    fireEvent.change(input, { target: { value: '{"url":"https://defos.ai"}' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Start operation' }));
    fireEvent.submit(dialog.querySelector('form')!);
    expect(within(dialog).getByRole('button', { name: 'Sending…' })).toBeDisabled();
    expect(api.posts()).toHaveLength(2);
    const second = JSON.parse(api.posts()[1][1]!.body as string);
    expect(second).toEqual({ operation_id: first.operation_id, mission_name: mission.name, input: { url: 'https://defos.ai' } });
    expect(first.operation_id).toMatch(/^[a-f\d]{8}-(?:[a-f\d]{4}-){3}[a-f\d]{12}$/);
    await act(async () => release());

    expect(await screen.findByRole('heading', { level: 1, name: first.operation_id })).toBeVisible();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByText('Operation created. Commander can now plan its first task.')).toBeVisible();
    expect(api.posts().every(([url]) => String(url).endsWith('/operations'))).toBe(true);
  });

  it('keeps an uncertain start from being resubmitted and offers to inspect its stable operation ID', async () => {
    const api = fixture((_url, body) => {
      api.state.current = { ...operation, id: body.operation_id as string, questions: [] };
      return Response.json({ error: 'command_outcome_unknown', detail: 'private details' }, { status: 503 });
    });
    view();
    const dialog = await startDialog();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Start operation' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('command outcome is unknown');
    expect(within(dialog).getByRole('button', { name: 'Start operation' })).toBeDisabled();
    expect(within(dialog).getByLabelText('Mission input (JSON)')).toBeDisabled();
    expect(within(dialog).queryByText('private details')).not.toBeInTheDocument();
    fireEvent.submit(dialog.querySelector('form')!);
    expect(api.posts()).toHaveLength(1);
    fireEvent.click(within(dialog).getByRole('button', { name: 'View operation' }));

    expect(await screen.findByRole('heading', { level: 1, name: api.state.current.id })).toBeVisible();
    expect(api.posts()).toHaveLength(1);
  });

  it('confirms cancellation and removes controls once the operation is terminal', async () => {
    const api = fixture(() => { api.state.current = { ...operation, status: 'cancelled' }; return Response.json(api.state.current); });
    view(); await detail();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel operation' }));
    let dialog = screen.getByRole('dialog', { name: 'Cancel operation' });
    expect(dialog).toHaveTextContent('Tasks already running may finish');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    expect(api.posts()).toHaveLength(0);

    fireEvent.click(screen.getByRole('button', { name: 'Cancel operation' }));
    dialog = screen.getByRole('dialog', { name: 'Cancel operation' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel operation' }));

    expect(await screen.findByText('Cancelled', { selector: '.catalog-status' })).toBeVisible();
    expect(api.posts()).toHaveLength(1);
    expect(api.posts()[0][0]).toBe(`${connection.url}/api/v1/operations/${operation.id}/cancel`);
    expect(screen.queryByRole('button', { name: 'Cancel operation' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Answer question' })).not.toBeInTheDocument();
  });

  it('keeps command notices on their operation when browser navigation changes the selection', async () => {
    const api = fixture(() => { api.state.current = { ...operation, status: 'cancelled' }; return Response.json(api.state.current); });
    const props = { connection, credentials, onSignIn: vi.fn(), onSelect: vi.fn() };
    const rendered = render(<OperationsPage {...props} selectedID={operation.id} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel operation' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel operation' }));
    expect(await screen.findByText('Operation cancelled. Already-dispatched work may still finish.')).toBeVisible();

    rendered.rerender(<OperationsPage {...props} />);
    expect(screen.queryByText('Operation cancelled. Already-dispatched work may still finish.')).not.toBeInTheDocument();
    api.state.current = { ...operation, id: 'other-operation' };
    rendered.rerender(<OperationsPage {...props} selectedID="other-operation" />);
    await screen.findByRole('button', { name: 'Cancel operation' });
    expect(screen.queryByText('Operation cancelled. Already-dispatched work may still finish.')).not.toBeInTheDocument();
  });

  it.each(['option', 'custom'])('submits a question %s answer and refreshes the saved answer', async mode => {
    const api = fixture((_url, body) => {
      api.state.current.questions![0] = { ...operation.questions![0], status: 'answered', answer: body.answer as string, answered_at: now };
      return Response.json(api.state.current);
    });
    view(); await detail();
    fireEvent.click(screen.getByRole('button', { name: 'Answer question' }));
    const dialog = screen.getByRole('dialog', { name: 'Answer question' });
    expect(within(dialog).getByRole('button', { name: 'Send answer' })).toBeDisabled();
    if (mode === 'option') fireEvent.click(within(dialog).getByRole('radio', { name: 'Use HTTP checks' }));
    else fireEvent.change(within(dialog).getByLabelText('Your answer'), { target: { value: 'Proceed with available evidence.' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send answer' }));

    expect(await screen.findByText('Answered', { selector: '.catalog-status' })).toBeVisible();
    expect(api.posts()).toHaveLength(1);
    expect(JSON.parse(api.posts()[0][1]!.body as string)).toEqual({ answer: mode === 'option' ? 'Use HTTP checks' : 'Proceed with available evidence.' });
    expect(screen.queryByRole('button', { name: 'Answer question' })).not.toBeInTheDocument();
  });

  it('disables an open answer form when its deadline passes', async () => {
    vi.useFakeTimers();
    try {
      const api = fixture();
      api.state.current.questions![0].expires_at = new Date(Date.now() + 2000).toISOString();
      await act(async () => { view(); });
      fireEvent.click(screen.getByRole('button', { name: `View operation ${operation.id}` }));
      await act(async () => {});
      fireEvent.click(screen.getByRole('button', { name: 'Answer question' }));
      const dialog = screen.getByRole('dialog');
      fireEvent.click(within(dialog).getByRole('radio', { name: 'Use HTTP checks' }));
      expect(within(dialog).getByRole('button', { name: 'Send answer' })).toBeEnabled();

      await act(async () => { await vi.advanceTimersByTimeAsync(2001); });

      expect(within(dialog).getByRole('button', { name: 'Send answer' })).toBeDisabled();
      expect(dialog).toHaveTextContent('This question has expired');
      fireEvent.submit(dialog.querySelector('form')!);
      expect(api.posts()).toHaveLength(0);
      fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
      expect(screen.queryByRole('button', { name: 'Answer question' })).not.toBeInTheDocument();
    } finally { vi.useRealTimers(); }
  });

  it('handles another operator answering first without retrying or overwriting their answer', async () => {
    const api = fixture(() => {
      api.state.current.questions![0] = { ...operation.questions![0], status: 'answered', answer: 'Answer from another operator', answered_at: now };
      return Response.json({ error: 'question_answered' }, { status: 409 });
    });
    view(); await detail();
    fireEvent.click(screen.getByRole('button', { name: 'Answer question' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Use HTTP checks' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send answer' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('already been answered');
    fireEvent.submit(dialog.querySelector('form')!);
    expect(api.posts()).toHaveLength(1);
    fireEvent.click(within(dialog).getByRole('button', { name: 'View operation' }));
    expect(await screen.findByText('Answer from another operator')).toBeVisible();
  });

  it('aborts a submitted request and suppresses late updates when credentials are forgotten', async () => {
    let finish!: (response: Response) => void;
    let signal: AbortSignal | null | undefined;
    const api = fixture((_url, _body, requestSignal) => { signal = requestSignal; return new Promise<Response>(resolve => { finish = resolve; }); });
    const rendered = view(); await detail();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel operation' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel operation' }));
    expect(signal?.aborted).toBe(false);

    rendered.rerender(<OperationsPage connection={connection} onSignIn={vi.fn()} />);
    expect(signal?.aborted).toBe(true);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await act(async () => finish(Response.json({ ...operation, status: 'cancelled' })));

    expect(screen.getByRole('alert')).toHaveTextContent('Sign in to view operations');
    expect(screen.queryByText('Operation cancelled. Already-dispatched work may still finish.')).not.toBeInTheDocument();
    expect(api.posts()).toHaveLength(1);
  });
});

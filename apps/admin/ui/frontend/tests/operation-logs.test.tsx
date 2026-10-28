import { act, cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/api/client';
import { OperationLogsPanel } from '../src/components/OperationLogsPanel';
import { documentVisibility, logScrollPosition, operationLogAPI } from './fakes/operation-logs';
import {
  logCredentials, logOperationID, logTaskID, operationLog, operationLogs,
  otherLogOperationID, otherLogTaskID, renderOperationLogs,
} from './fixtures/operation-logs';

vi.mock('../src/api/operation-logs', () => ({ listOperationLogs: vi.fn() }));

function messages() {
  return within(screen.getByRole('log', { name: 'Log entries' })).queryAllByText(/^Progress \d+$/)
    .map(element => element.textContent);
}

describe('operation log viewer', () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { cleanup(); vi.clearAllTimers(); vi.useRealTimers(); });

  it('opens the newest page in append order and follows its newest ID after finished tasks', async () => {
    const api = operationLogAPI();
    renderOperationLogs();

    expect(api.request(0).query).toMatchObject({ order: 'desc', cursor: '', limit: 100 });
    expect(api.request(0).operationID).toBe(logOperationID);
    await act(async () => { api.request(0).reply(operationLogs(1, 3).reverse()); });

    expect(messages()).toEqual(['Progress 1', 'Progress 2', 'Progress 3']);
    expect(screen.getByRole('log', { name: 'Log entries' })).toHaveAttribute('aria-live', 'off');
    expect(screen.getByRole('button', { name: 'Load older' })).toBeDisabled();

    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });

    expect(api.request(1).query).toMatchObject({ order: 'asc', cursor: operationLog(3).id, limit: 100 });
    await act(async () => { api.request(1).reply([operationLog(4)]); });

    expect(messages()).toEqual(['Progress 1', 'Progress 2', 'Progress 3', 'Progress 4']);
  });

  it('waits for each request to settle before scheduling another poll', async () => {
    const api = operationLogAPI();
    renderOperationLogs();
    await act(async () => { api.request(0).reply([operationLog(1)]); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });

    await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });

    expect(api.count()).toBe(2);
    await act(async () => { api.request(1).reply([]); });
    await act(async () => { await vi.advanceTimersByTimeAsync(1999); });
    expect(api.count()).toBe(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(api.request(2).query.cursor).toBe(operationLog(1).id);
  });

  it('pauses for older history and resumes from the visible newest entry', async () => {
    const api = operationLogAPI();
    renderOperationLogs();
    await act(async () => { api.request(0).reply(operationLogs(101, 200).reverse()); });

    fireEvent.click(screen.getByRole('button', { name: 'Load older' }));

    expect(screen.getByRole('button', { name: 'Follow live' })).toBeEnabled();
    expect(api.request(1).query).toMatchObject({ order: 'desc', cursor: operationLog(101).id });
    await act(async () => { api.request(1).reply(operationLogs(1, 100).reverse()); });
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(api.count()).toBe(2);
    expect(messages()).toHaveLength(200);
    expect(messages().at(0)).toBe('Progress 1');
    expect(messages().at(-1)).toBe('Progress 200');

    fireEvent.click(screen.getByRole('button', { name: 'Follow live' }));

    expect(api.request(2).query).toMatchObject({ order: 'asc', cursor: operationLog(200).id });
  });

  it('aborts an initial request on pause and ignores its late response after resuming', async () => {
    const api = operationLogAPI();
    renderOperationLogs();
    const initial = api.request(0);

    fireEvent.click(screen.getByRole('button', { name: 'Pause following' }));
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });

    expect(initial.signal?.aborted).toBe(true);
    expect(api.count()).toBe(1);

    fireEvent.click(screen.getByRole('button', { name: 'Follow live' }));
    expect(api.request(1).query).toMatchObject({ order: 'desc', cursor: '' });
    await act(async () => { api.request(1).reply([operationLog(2)]); });
    await act(async () => { initial.reply([operationLog(1)]); });

    expect(messages()).toEqual(['Progress 2']);
  });

  it('pauses when the reader scrolls up and returns to the bottom after following resumes', async () => {
    const api = operationLogAPI();
    renderOperationLogs();
    const area = screen.getByRole('log', { name: 'Log entries' });
    const scroll = logScrollPosition(area);
    await act(async () => { api.request(0).reply(operationLogs(1, 3).reverse()); });
    expect(scroll.top()).toBe(scroll.bottom);

    area.scrollTop = 100;
    fireEvent.scroll(area);
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });

    expect(screen.getByRole('button', { name: 'Follow live' })).toBeEnabled();
    expect(scroll.top()).toBe(100);
    expect(api.count()).toBe(1);

    fireEvent.click(screen.getByRole('button', { name: 'Follow live' }));
    await act(async () => { api.request(1).reply([operationLog(4)]); });

    expect(scroll.top()).toBe(scroll.bottom);
    expect(messages().at(-1)).toBe('Progress 4');
  });

  it('suspends hidden requests without losing following mode and catches up when visible', async () => {
    const visibility = documentVisibility();
    const api = operationLogAPI();
    renderOperationLogs();
    await act(async () => { api.request(0).reply([operationLog(1)]); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    const pending = api.request(1);

    act(() => { visibility.set('hidden'); });
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000); pending.reply([operationLog(2)]); });

    expect(pending.signal?.aborted).toBe(true);
    expect(api.count()).toBe(2);
    expect(messages()).toEqual(['Progress 1']);
    expect(screen.getByRole('button', { name: 'Pause following' })).toBeEnabled();

    act(() => { visibility.set('visible'); });
    expect(api.request(2).query).toMatchObject({ order: 'asc', cursor: operationLog(1).id });
    await act(async () => { api.request(2).reply([operationLog(2)]); });

    expect(messages()).toEqual(['Progress 1', 'Progress 2']);
  });

  it('applies and clears task, agent, and exact action filters only on submission', async () => {
    const api = operationLogAPI();
    const changed = vi.fn();
    const view = renderOperationLogs({ onFiltersChange: changed });
    await act(async () => { api.request(0).reply([operationLog(1)]); });

    fireEvent.change(screen.getByLabelText('Task'), { target: { value: otherLogTaskID } });
    fireEvent.change(screen.getByLabelText('Agent ID'), { target: { value: 'builder-agent-87654321' } });
    fireEvent.change(screen.getByLabelText('Action'), { target: { value: 'Publish Artifact' } });

    expect(api.count()).toBe(1);
    fireEvent.submit(screen.getByRole('form', { name: 'Log filters' }));
    const filters = { task_id: otherLogTaskID, agent_id: 'builder-agent-87654321', action_name: 'Publish Artifact' };
    expect(changed).toHaveBeenLastCalledWith(filters);
    view.rerender(<OperationLogsPanel {...view.props} filters={filters} />);

    expect(api.request(1).query).toMatchObject({ ...filters, order: 'desc', cursor: '' });
    expect(messages()).toEqual([]);
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(changed).toHaveBeenLastCalledWith({});
    view.rerender(<OperationLogsPanel {...view.props} filters={{}} />);

    expect(api.request(1).signal?.aborted).toBe(true);
    expect(api.request(2).query).toMatchObject({ order: 'desc', cursor: '' });
    expect(screen.getByLabelText('Task')).toHaveValue('');
    expect(screen.getByLabelText('Agent ID')).toHaveValue('');
    expect(screen.getByLabelText('Action')).toHaveValue('');
  });

  it('rejects invalid local filters without replacing the visible log scope', async () => {
    const api = operationLogAPI();
    renderOperationLogs();
    await act(async () => { api.request(0).reply([operationLog(1)]); });

    fireEvent.change(screen.getByLabelText('Agent ID'), { target: { value: 'invalid-agent' } });
    fireEvent.submit(screen.getByRole('form', { name: 'Log filters' }));

    expect(screen.getByRole('alert')).toBeVisible();
    expect(api.count()).toBe(1);
    expect(messages()).toEqual(['Progress 1']);
  });

  it('aborts and clears stale work on filter, credential, URL, operation and unmount changes', async () => {
    const api = operationLogAPI();
    const view = renderOperationLogs({ onFiltersChange: vi.fn() });
    await act(async () => { api.request(0).reply([operationLog(1)]); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });

    const filters = { task_id: logTaskID };
    view.rerender(<OperationLogsPanel {...view.props} filters={filters} />);
    expect(api.request(1).signal?.aborted).toBe(true);
    expect(messages()).toEqual([]);
    expect(api.request(2).query).toMatchObject({ ...filters, order: 'desc', cursor: '' });
    const credentials = { ...logCredentials, username: 'another-operator' };
    view.rerender(<OperationLogsPanel {...view.props} filters={filters} credentials={credentials} />);
    expect(api.request(2).signal?.aborted).toBe(true);
    expect(api.request(3).credentials.username).toBe('another-operator');
    const url = 'https://another.example.test';
    view.rerender(<OperationLogsPanel {...view.props} filters={filters} credentials={credentials} url={url} />);
    expect(api.request(3).signal?.aborted).toBe(true);
    expect(api.request(4).url).toBe(url);
    view.rerender(<OperationLogsPanel {...view.props} filters={filters} credentials={credentials} url={url} operationID={otherLogOperationID} />);
    expect(api.request(4).signal?.aborted).toBe(true);
    expect(api.request(5).operationID).toBe(otherLogOperationID);
    await act(async () => {
      for (let index = 1; index <= 4; index++) api.request(index).reply([operationLog(index)]);
      api.request(5).reply([operationLog(50, { operation_id: otherLogOperationID })]);
    });

    expect(messages()).toEqual(['Progress 50']);
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    const pending = api.request(6);
    view.unmount();
    await act(async () => { pending.reply([operationLog(51)]); await vi.advanceTimersByTimeAsync(20_000); });

    expect(pending.signal?.aborted).toBe(true);
    expect(api.count()).toBe(7);
  });

  it('retains entries and the follow cursor through sanitized query failures and explicit retry', async () => {
    const api = operationLogAPI();
    renderOperationLogs();
    await act(async () => { api.request(0).reply([operationLog(1)]); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });

    await act(async () => { api.request(1).fail(new Error('password=private-upstream-value')); });

    expect(screen.getByRole('alert')).not.toHaveTextContent('private-upstream-value');
    expect(messages()).toEqual(['Progress 1']);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(api.request(2).query).toMatchObject({ order: 'asc', cursor: operationLog(1).id });
    await act(async () => { api.request(2).reply([operationLog(2)]); });

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(messages()).toEqual(['Progress 1', 'Progress 2']);
  });

  it('rejects an overlapping full catch-up page without repeatedly requesting the same cursor', async () => {
    const api = operationLogAPI();
    renderOperationLogs();
    await act(async () => { api.request(0).reply(operationLogs(1, 100).reverse()); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });

    await act(async () => { api.request(1).reply(operationLogs(0, 99)); });
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });

    expect(api.count()).toBe(2);
    expect(screen.getByRole('alert')).toBeVisible();
    expect(messages()).toHaveLength(100);
    expect(messages().at(0)).toBe('Progress 1');
    expect(messages().at(-1)).toBe('Progress 100');

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

    expect(api.request(2).query).toMatchObject({ order: 'asc', cursor: operationLog(100).id });
  });

  it('offers sign-in without reading logs when credentials are missing', () => {
    const api = operationLogAPI();
    const signIn = vi.fn();
    renderOperationLogs({ credentials: undefined, onSignIn: signIn });

    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(api.count()).toBe(0);
    expect(signIn).toHaveBeenCalledOnce();
    expect(screen.getByRole('button', { name: 'Follow live' })).toBeDisabled();
  });

  it('stops automatic polling after authentication failure and offers sign-in', async () => {
    const api = operationLogAPI();
    const signIn = vi.fn();
    renderOperationLogs({ onSignIn: signIn });

    await act(async () => { api.request(0).fail(new ApiError('authentication')); });
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(api.count()).toBe(1);
    expect(signIn).toHaveBeenCalledOnce();
  });

  it('renders log text as text and exposes its timestamp and execution provenance', async () => {
    const api = operationLogAPI();
    const text = '<script>window.stolen=true</script>\n<img src=x onerror="alert(1)">';
    const entry = operationLog(1, { message: text });
    renderOperationLogs();

    await act(async () => { api.request(0).reply([entry]); });

    const row = within(screen.getByRole('log', { name: 'Log entries' })).getByRole('listitem');
    expect(row.querySelector('pre')?.textContent).toBe(text);
    expect(row.querySelector('script, img')).toBeNull();
    expect(row).toHaveTextContent(entry.agent_id);
    expect(row).toHaveTextContent(entry.action_name);
    expect(row).toHaveTextContent(entry.task_id);
    expect(row.querySelector('time')).toHaveAttribute('datetime', entry.created_at);
  });

  it('drains full live pages, bounds history, catches up after older reads and jumps to latest', async () => {
    const api = operationLogAPI();
    renderOperationLogs();
    await act(async () => { api.request(0).reply(operationLogs(1, 100).reverse()); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });

    for (let batch = 1; batch <= 10; batch++) {
      expect(api.request(batch).query).toMatchObject({ order: 'asc', cursor: operationLog(batch * 100).id });
      await act(async () => { api.request(batch).reply(operationLogs(batch * 100 + 1, batch * 100 + 100)); });
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    }
    expect(api.count()).toBe(11);
    await act(async () => { await vi.advanceTimersByTimeAsync(1999); });
    expect(api.count()).toBe(11);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    await act(async () => { api.request(11).reply([]); });

    expect(messages()).toHaveLength(1000);
    expect(messages().at(0)).toBe('Progress 101');
    expect(messages().at(-1)).toBe('Progress 1100');
    expect(screen.getByText(/entries were omitted/)).toBeVisible();

    fireEvent.click(screen.getByRole('button', { name: 'Load older' }));
    expect(api.request(12).query).toMatchObject({ order: 'desc', cursor: operationLog(101).id });
    await act(async () => { api.request(12).reply(operationLogs(1, 100).reverse()); });
    expect(messages()).toHaveLength(1000);
    expect(messages().at(0)).toBe('Progress 1');
    expect(messages().at(-1)).toBe('Progress 1000');

    fireEvent.click(screen.getByRole('button', { name: 'Follow live' }));
    expect(api.request(13).query).toMatchObject({ order: 'asc', cursor: operationLog(1000).id });
    await act(async () => { api.request(13).reply(operationLogs(1001, 1100)); });
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(api.request(14).query.cursor).toBe(operationLog(1100).id);

    fireEvent.click(screen.getByRole('button', { name: 'Jump to latest' }));
    expect(api.request(14).signal?.aborted).toBe(true);
    expect(api.request(15).query).toMatchObject({ order: 'desc', cursor: '' });
    await act(async () => { api.request(15).reply(operationLogs(1901, 2000).reverse()); });

    expect(messages()).toHaveLength(100);
    expect(messages().at(0)).toBe('Progress 1901');
    expect(messages().at(-1)).toBe('Progress 2000');
    expect(screen.queryByText(/entries were omitted/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Pause following' })).toBeEnabled();
  }, 15_000);
});

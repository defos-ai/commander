import { describe, expect, it } from 'vitest';
import { ApiError } from '../src/api/client';
import { listOperationLogs } from '../src/api/operation-logs';
import type { OperationLogQuery } from '../src/api/operation-logs';
import { respondJSON, respondSequence } from './fakes/fetch';
import {
  agentID, credentials, cursorID, invalidLogPatches, invalidQueries, makeLog, makePage,
  nextLogID, operationID, otherAgentID, otherTaskID, taskID, url, yearZeroTimestamp,
} from './fixtures/operation-log-client';

function proxyPath(input: RequestInfo | URL) {
  const endpoint = new URL(String(input), window.location.origin);
  expect(endpoint.origin).toBe(window.location.origin);
  expect(endpoint.pathname).toBe('/ui/proxy');
  expect(endpoint.searchParams.get('url')).toBe(url);
  expect([...endpoint.searchParams.keys()]).toEqual(['url', 'path']);
  return endpoint.searchParams.get('path');
}

describe('operation log client', () => {
  it('reads flat log provenance through the authenticated same-origin proxy', async () => {
    const page = makePage(makeLog());
    const fetch = respondJSON(page);

    const result = await listOperationLogs(`${url}/`, credentials, operationID);

    expect(result).toEqual(page);
    expect(fetch).toHaveBeenCalledOnce();
    expect(proxyPath(fetch.mock.calls[0][0])).toBe(`/api/v1/operations/${operationID}/logs`);
    expect(fetch.mock.calls[0][1]).toMatchObject({
      method: 'GET', headers: { Accept: 'application/json', 'X-Commander-UI': '1' },
      credentials: 'same-origin', redirect: 'error', cache: 'no-store',
    });
    expect(fetch.mock.calls[0][1]?.headers).not.toHaveProperty('Authorization');
  });

  it.each(['asc', 'desc'] as const)('preserves %s server order despite skewed timestamps and UUIDs', async order => {
    const first = makeLog({ created_at: '2026-10-28T13:00:00Z' });
    const last = makeLog({ id: nextLogID, created_at: '2026-10-28T11:00:00Z' });
    const page = makePage(first, last);
    const fetch = respondJSON(page);

    const result = await listOperationLogs(url, credentials, operationID, { order, cursor: cursorID, limit: 2 });

    expect(result).toEqual(page);
    expect(proxyPath(fetch.mock.calls[0][0])).toBe(`/api/v1/operations/${operationID}/logs?order=${order}&cursor=${cursorID}&limit=2`);
  });

  it('encodes all exact filters without trimming action names or message text', async () => {
    const log = makeLog({ action_name: ' Build / #é? ', message: ' phase\n details 🛠 \n' });
    const fetch = respondJSON(makePage(log));
    const query = { task_id: taskID, agent_id: agentID, action_name: log.action_name, order: 'desc', cursor: '', limit: 1000 } as const;

    const result = await listOperationLogs(url, credentials, operationID, query);

    expect(result.items).toEqual([log]);
    const endpoint = new URL(proxyPath(fetch.mock.calls[0][0])!, window.location.origin);
    expect(Object.fromEntries(endpoint.searchParams)).toEqual({ ...query, limit: '1000' });
  });

  it('keeps an empty page cursor so a later append can be requested with the same anchor', async () => {
    const later = makeLog({ id: nextLogID });
    const fetch = respondSequence({ items: [], next_cursor: cursorID }, makePage(later));

    const empty = await listOperationLogs(url, credentials, operationID, { cursor: cursorID });
    const next = await listOperationLogs(url, credentials, operationID, { cursor: empty.next_cursor });

    expect(empty).toEqual({ items: [], next_cursor: cursorID });
    expect(next.items).toEqual([later]);
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(proxyPath(fetch.mock.calls[1][0])).toBe(`/api/v1/operations/${operationID}/logs?cursor=${cursorID}`);
  });

  it('checks the response against the filters actually sent when caller state changes', async () => {
    const log = makeLog();
    respondJSON(makePage(log));
    const query = { task_id: taskID, limit: 1 };

    const pending = listOperationLogs(url, credentials, operationID, query);
    query.task_id = otherTaskID;
    const page = await pending;

    expect(page.items).toEqual([log]);
    expect(query.task_id).toBe(otherTaskID);
  });

  it('accepts an empty first page and the exact UTF-8 byte limit', async () => {
    const log = makeLog({ message: '🛠'.repeat(512), created_at: '2024-02-29T12:00:00.000000001+02:00' });
    respondSequence(makePage(), makePage(log));

    const empty = await listOperationLogs(url, credentials, operationID);
    const full = await listOperationLogs(url, credentials, operationID);

    expect(empty).toEqual({ items: [], next_cursor: '' });
    expect(full.items).toEqual([log]);
  });

  it('accepts Go year-zero timestamps and preserves their nanoseconds', async () => {
    const log = makeLog({ created_at: yearZeroTimestamp });
    respondJSON(makePage(log));

    const page = await listOperationLogs(url, credentials, operationID);

    expect(page.items).toEqual([log]);
    expect(page.items[0].created_at).toBe(yearZeroTimestamp);
  });

  it.each(invalidLogPatches)('rejects malformed or out-of-operation log fields: %j', async patch => {
    const item = { ...makeLog(), ...patch };
    respondJSON({ items: [item], next_cursor: item.id });

    await expect(listOperationLogs(url, credentials, operationID)).rejects.toEqual(new ApiError('invalid_response'));
  });

  it.each([
    { task_id: otherTaskID }, { agent_id: otherAgentID }, { action_name: 'publish-agent' },
  ])('rejects records outside an exact filter: %j', async query => {
    respondJSON(makePage(makeLog()));

    await expect(listOperationLogs(url, credentials, operationID, query)).rejects.toEqual(new ApiError('invalid_response'));
  });

  it.each([
    null, [], {}, { items: null, next_cursor: '' }, { items: [null], next_cursor: '' },
    { items: [], next_cursor: null }, { items: [], next_cursor: cursorID },
    { items: [makeLog()], next_cursor: cursorID },
    { items: [makeLog(), makeLog()], next_cursor: makeLog().id },
  ])('rejects ambiguous page shapes and cursor claims: %j', async body => {
    respondJSON(body);

    await expect(listOperationLogs(url, credentials, operationID)).rejects.toEqual(new ApiError('invalid_response'));
  });

  it('rejects replay of the exclusive cursor and oversized pages', async () => {
    const first = makeLog(), second = makeLog({ id: nextLogID });
    respondSequence(makePage(first), makePage(first, second), { items: [], next_cursor: '' });

    await expect(listOperationLogs(url, credentials, operationID, { cursor: first.id })).rejects.toEqual(new ApiError('invalid_response'));
    await expect(listOperationLogs(url, credentials, operationID, { limit: 1 })).rejects.toEqual(new ApiError('invalid_response'));
    await expect(listOperationLogs(url, credentials, operationID, { cursor: cursorID })).rejects.toEqual(new ApiError('invalid_response'));
  });

  it.each(invalidQueries)('rejects invalid query input before fetching: %j', async query => {
    const fetch = respondJSON(makePage());

    await expect(listOperationLogs(url, credentials, operationID, query as OperationLogQuery)).rejects.toEqual(new ApiError('invalid_response'));

    expect(fetch).not.toHaveBeenCalled();
  });

  it.each(['', '../operations', '11111111111141118111111111111111', 'AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA'])('rejects a noncanonical operation ID before fetching: %s', async id => {
    const fetch = respondJSON(makePage());

    await expect(listOperationLogs(url, credentials, id)).rejects.toEqual(new ApiError('invalid_response'));

    expect(fetch).not.toHaveBeenCalled();
  });

  it.each([[401, 'authentication'], [403, 'forbidden'], [404, 'not_found'], [503, 'unavailable']] as const)('preserves shared API error handling for HTTP %s', async (status, kind) => {
    const fetch = respondJSON({}, status);

    await expect(listOperationLogs(url, credentials, operationID)).rejects.toEqual(new ApiError(kind));

    expect(fetch).toHaveBeenCalledOnce();
  });

  it('honors cancellation before fetching', async () => {
    const fetch = respondJSON(makePage());
    const controller = new AbortController();
    controller.abort();

    await expect(listOperationLogs(url, credentials, operationID, {}, controller.signal)).rejects.toMatchObject({ name: 'AbortError' });

    expect(fetch).not.toHaveBeenCalled();
  });
});

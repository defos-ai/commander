import { describe, expect, it, vi } from 'vitest';
import { getOperation, listOperations } from '../src/api/operations';
import type { Operation } from '../src/api/operation-types';

const url = 'https://commander.example/team';
const credentials = { url, username: 'operator' };
const timestamp = '2026-10-05T12:00:00.123456789Z';
const operation: Operation = {
  id: 'operation-1',
  mission: {
    name: 'review-website-with-browser', objective: 'Review a website', required_actions: ['check-links'],
    starting_actions: ['discover-pages'], input_schema: null, output_schema: null,
  },
  input: { url: 'https://example.com' }, status: 'waiting',
  preflight: {
    ready: false, version: 2, actions: [{
      action_name: 'capture-page', ready_agent_ids: ['agent-1'], unavailable_agents: [{
        agent_id: 'agent-2', agent_type: 'browser', reason: 'Browser missing', missing_requirements: [{ kind: 'executable', name: 'chromium' }],
        missing_secrets: ['SCREENSHOT_TOKEN'],
      }],
    }],
  },
  tasks: [{
    id: 'task-1', operation_id: 'operation-1', parent_task_id: 'task-0', action_name: 'check-links',
    agent_id: 'agent-1', command_id: 'command-1', input: { url: 'https://example.com' },
    status: 'succeeded', started_at: timestamp, finished_at: timestamp,
    result: {
      action_name: 'check-links', output: { broken_links: 0 },
      artifacts: [{
        id: 'artifact-1', name: 'Links report', agent_id: 'agent-1', action_name: 'check-links', created_at: timestamp,
        files: [{ id: 'file-1', name: 'links.json', media_type: 'application/json', size: 42, sha256: 'abc' }],
      }],
      next_actions: [{ action_name: 'capture-page', input: { url: 'https://example.com' }, reason: 'Review rendering' }],
    },
    continuation: {
      version: 2, decision_by: 'operator', status: 'awaiting_operator',
      proposed_actions: [{ action_name: 'capture-page' }], created_task_ids: [], question_id: 'question-1',
    },
  }],
  questions: [{
    id: 'question-1', task_id: 'task-1', status: 'pending', text: 'Continue with rendering checks?',
    options: ['Yes', 'No'], asked_at: timestamp, expires_at: timestamp,
  }],
  events: [{ sequence: 1, operation_id: 'operation-1', type: 'task.finished', status: 'waiting', task_id: 'task-1', created_at: timestamp }],
  created_at: timestamp, updated_at: timestamp,
};

function proxyTarget(input: RequestInfo | URL) {
  const endpoint = new URL(String(input), window.location.origin);
  expect(endpoint.origin).toBe(window.location.origin);
  expect(endpoint.pathname).toBe('/ui/proxy');
  return Object.fromEntries(endpoint.searchParams);
}

function respond(...bodies: unknown[]) {
  const fetch = vi.fn<typeof globalThis.fetch>();
  for (const body of bodies) fetch.mockResolvedValueOnce(Response.json(body));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

describe('operations client', () => {
  it('preserves hydrated tasks, decisions, dependencies and artifact metadata through authenticated GETs', async () => {
    const fetch = respond({ items: [operation] }, operation);

    const items = await listOperations(url, credentials);
    const detail = await getOperation(url, credentials, operation.id);

    expect(items).toEqual([operation]);
    expect(detail).toEqual(operation);
    expect(fetch.mock.calls.map(([path]) => proxyTarget(path))).toEqual([
      { url, path: '/api/v1/operations' }, { url, path: `/api/v1/operations/${operation.id}` },
    ]);
    for (const [, options] of fetch.mock.calls) {
      expect(options).toMatchObject({
        method: 'GET', credentials: 'same-origin', redirect: 'error', cache: 'no-store',
        headers: { Accept: 'application/json', 'X-Commander-UI': '1' },
      });
      expect(new Headers(options?.headers).has('Authorization')).toBe(false);
    }
  });

  it('accepts an empty list and nullable Go slices with arbitrary raw JSON', async () => {
    const nullable: Operation = {
      ...operation, input: ['any', 42, false], output: 'a plain text result', status: 'future-status',
      preflight: null, tasks: null, questions: null, events: null,
      created_at: '0001-01-01T00:00:00Z', updated_at: '0001-01-01T00:00:00Z',
    };
    respond({ items: [] }, nullable);

    expect(await listOperations(url, credentials)).toEqual([]);
    expect(await getOperation(url, credentials, operation.id)).toEqual(nullable);
  });

  it('accepts queued tasks, optional nulls, nullable continuations and future status values', async () => {
    const nullable: Operation = {
      ...operation,
      preflight: { ready: false, version: 0, actions: null },
      tasks: [{
        id: 'task-1', operation_id: operation.id, agent_id: '', action_name: 'check-links', status: 'preparing',
        input: null, started_at: '0001-01-01T00:00:00Z', deadline_at: null, finished_at: null,
        result: { action_name: 'check-links', output: null, artifacts: null, next_actions: null },
        continuation: { version: 1, decision_by: 'future-decider', status: 'future-status', proposed_actions: null, created_task_ids: null },
      }],
      questions: [{ id: 'question-1', status: 'expired', text: 'Continue?', options: null, asked_at: timestamp, answered_at: null, expires_at: timestamp }],
    };
    respond(nullable);

    expect(await getOperation(url, credentials, operation.id)).toEqual(nullable);
  });

  it('encodes the operation ID as one path segment under the configured base URL', async () => {
    const id = 'team/operation ?#é';
    const fetch = respond({ ...operation, id });

    await getOperation(`${url}/`, credentials, id);

    expect(proxyTarget(fetch.mock.calls[0][0])).toEqual({ url, path: '/api/v1/operations/team%2Foperation%20%3F%23%C3%A9' });
  });

  it('rejects invalid IDs before contacting the server and rejects mismatched details', async () => {
    const fetch = respond({ ...operation, id: 'different-operation' });

    for (const id of ['', ' ', '.', '..', '\ud800']) {
      await expect(getOperation(url, credentials, id)).rejects.toMatchObject({ kind: 'invalid_response' });
    }
    expect(fetch).not.toHaveBeenCalled();
    await expect(getOperation(url, credentials, operation.id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it.each([
    { mission: { ...operation.mission, starting_actions: 'discover-pages' } },
    { tasks: [{ ...operation.tasks![0], result: { action_name: 'check-links', output: null, artifacts: [{ id: 'artifact-1', files: 'unexpected' }] } }] },
    { tasks: [{ ...operation.tasks![0], continuation: { ...operation.tasks![0].continuation, created_task_ids: [42] } }] },
    { tasks: [{ ...operation.tasks![0], finished_at: 'not a date' }] },
    { questions: [{ ...operation.questions![0], options: [42] }] },
    { preflight: { ready: false, version: 1, actions: [{ action_name: 'capture-page', unavailable_agents: [{ agent_id: 'agent-1', reason: 'Missing', missing_requirements: [{ kind: 'configuration', name: 'KEY', secret: 'yes' }] }] }] } },
    { events: [{ ...operation.events![0], sequence: -1 }] },
    { updated_at: 42 },
    { error: { message: 'failure' } },
  ])('rejects malformed nested data before rendering: %j', async patch => {
    respond({ items: [{ ...operation, ...patch }] });

    await expect(listOperations(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it('rejects invalid artifact file metadata even when the rest of the result is valid', async () => {
    const artifact = operation.tasks![0].result!.artifacts![0];
    const invalid = {
      ...operation,
      tasks: [{ ...operation.tasks![0], result: {
        ...operation.tasks![0].result, artifacts: [{ ...artifact, files: [{ ...artifact.files![0], size: -1 }] }],
      } }],
    };
    respond(invalid);

    await expect(getOperation(url, credentials, operation.id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it.each([
    { agent_type: 42 }, { agent_type: '' }, { agent_type: 'Builder' }, { agent_type: 'b'.repeat(64) },
    { agent_type: 'builder', missing_secrets: 'TOKEN' }, { agent_type: 'builder', missing_secrets: [42] },
    { agent_type: 'builder', missing_secrets: [''] }, { agent_type: 'builder', missing_secrets: ['token/name'] },
    { agent_type: 'builder', missing_secrets: ['a'.repeat(129)] }, { missing_secrets: ['TOKEN'] },
  ])('rejects malformed managed secret scopes: %j', async patch => {
    respond({ ...operation, preflight: { ready: false, version: 1, actions: [{ action_name: 'build',
      unavailable_agents: [{ agent_id: 'builder-agent-1234abcd', reason: 'Missing', ...patch }],
    }] } });

    await expect(getOperation(url, credentials, operation.id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it('accepts unavailable candidates without managed secret requirements', async () => {
    const body = { ...operation, preflight: { ready: false, version: 1, actions: [{ action_name: 'build', unavailable_agents: [
      { agent_id: 'agent-1', reason: 'Missing dependency' },
      { agent_id: 'agent-2', agent_type: 'builder', reason: 'Missing dependency', missing_secrets: null },
      { agent_id: 'agent-3', agent_type: 'builder', reason: 'Missing dependency', missing_secrets: [] },
    ] }] } };
    respond(body);

    expect(await getOperation(url, credentials, operation.id)).toEqual(body);
  });

  it.each([null, {}, { items: null }, { items: [null] }])('rejects a malformed list envelope: %j', async body => {
    respond(body);

    await expect(listOperations(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it.each([[401, 'authentication'], [403, 'forbidden'], [404, 'not_found'], [500, 'unavailable']] as const)(
    'surfaces HTTP %s without exposing the response body', async (status, kind) => {
      const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response('private server details', { status }));
      vi.stubGlobal('fetch', fetch);

      await expect(getOperation(url, credentials, operation.id)).rejects.toMatchObject({ kind, message: kind });
    },
  );
});

import { describe, expect, it } from 'vitest';
import type { Operation } from '../src/api/operation-types';
import { getOperation, listOperations } from '../src/api/operations';
import { respondSequence as respond } from './fakes/fetch';
import { credentials, operation, timestamp, url } from './fixtures/operation-client';

function proxyTarget(input: RequestInfo | URL) {
  const endpoint = new URL(String(input), window.location.origin);
  expect(endpoint.origin).toBe(window.location.origin);
  expect(endpoint.pathname).toBe('/ui/proxy');
  return Object.fromEntries(endpoint.searchParams);
}

describe('operations client', () => {
  it.each(['operator', 'model'] as const)('retains %s question answer provenance', async answeredBy => {
    const answered = { ...operation, questions: [{ ...operation.questions![0], status: 'answered', answer: 'Proceed', answered_by: answeredBy }] };
    respond(answered);

    const detail = await getOperation(url, credentials, operation.id);

    expect(detail.questions![0].answered_by).toBe(answeredBy);
  });

  it('requires a valid recipient type for runtime secret requirements', async () => {
    const task = { ...operation.tasks![0], status: 'running', agent_type: 'web-agent', missing_requirements: [{ kind: 'secret', name: 'TOKEN' }] };
    const waiting = { ...operation, tasks: [task] };
    respond(waiting, { ...operation, tasks: [{ ...task, agent_type: undefined }] });
    expect((await getOperation(url, credentials, operation.id)).tasks![0].missing_requirements).toEqual(task.missing_requirements);
    await expect(getOperation(url, credentials, operation.id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it('preserves hydrated tasks, decisions, dependencies and artifact metadata through authenticated GETs', async () => {
    const fetch = respond({ items: [operation] }, operation);

    const items = await listOperations(url, credentials);
    const detail = await getOperation(url, credentials, operation.id);

    expect(items).toEqual([operation]);
    expect(detail).toEqual(operation);
    expect(fetch.mock.calls.map(([path]) => proxyTarget(path))).toEqual([
      { url, path: '/api/v1/operations' }, { url, path: `/api/v1/operations/${operation.id}` },
    ]);
  });

  it('accepts an empty list and nullable Go slices with arbitrary raw JSON', async () => {
    const nullable: Operation = {
      ...operation, input: ['any', 42, false], output: 'a plain text result', status: 'future-status',
      progress: { task_counts: {}, total_tasks: 0, finished_tasks: 0, pending_decisions: 0, pending_questions: 0, settled: false },
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
      progress: { task_counts: { preparing: 1 }, total_tasks: 1, finished_tasks: 0, pending_decisions: 0, pending_questions: 0, settled: false },
      preflight: { ready: false, version: 0, actions: null },
      tasks: [{
        id: 'task-1', operation_id: operation.id, agent_id: '', action_name: 'check-links', status: 'preparing',
        input: null, started_at: '0001-01-01T00:00:00Z', deadline_at: null, finished_at: null,
        result: { action_name: 'check-links', output: null, artifacts: null, next_actions: null },
        continuation: { version: 1, decision_by: 'future-decider', status: 'future-status', proposed_actions: null, created_task_ids: null },
      }],
      questions: [{ id: 'question-1', status: 'pending', text: 'Continue?', options: null, asked_at: timestamp, answered_at: null, fallback_at: timestamp }],
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
    { progress: undefined },
    { progress: null },
    { progress: { ...operation.progress, task_counts: null } },
    { progress: { ...operation.progress, task_counts: [] } },
    { progress: { ...operation.progress, task_counts: { interrupted: -1 } } },
    { progress: { ...operation.progress, total_tasks: 0.5 } },
    { progress: { ...operation.progress, finished_tasks: '1' } },
    { progress: { ...operation.progress, pending_decisions: -1 } },
    { progress: { ...operation.progress, pending_questions: Number.MAX_SAFE_INTEGER + 1 } },
    { progress: { ...operation.progress, settled: 'true' } },
    { questions: [{ ...operation.questions![0], options: [42] }] },
    { questions: [{ ...operation.questions![0], answered_by: 'admin' }] },
    { questions: [{ ...operation.questions![0], answered_by: null }] },
    { preflight: { ready: false, version: 1, actions: [{ action_name: 'capture-page', unavailable_agents: [{ agent_id: 'agent-1', reason: 'Missing', missing_requirements: [{ kind: 'configuration', name: 'KEY', sensitive: 'yes' }] }] }] } },
    { events: [{ ...operation.events![0], sequence: -1 }] },
    { updated_at: 42 },
    { error: { message: 'failure' } },
    { affinity: { for_actions: [] } },
    { affinity: { for_actions: ['check-links', 'check-links'] } },
    { affinity: { for_actions: [42] } },
    { affinity: { for_actions: ['check-links'], agent_id: 42 } },
    { affinity: { for_actions: ['check-links'], agent_id: 'agent-1', error: 'Conflicting assignments' } },
    { affinity: { for_actions: ['check-links'], error: { message: 'Invalid binding' } } },
  ])('rejects malformed nested data before rendering: %j', async patch => {
    respond({ items: [{ ...operation, ...patch }] });

    await expect(listOperations(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it('preserves unbound affinity and a binding error without inventing an agent', async () => {
    for (const affinity of [
      { for_actions: ['check-links', 'capture-page'] },
      { for_actions: ['check-links'], error: 'Conflicting assignments' },
    ]) {
      respond({ ...operation, affinity });
      expect((await getOperation(url, credentials, operation.id)).affinity).toEqual(affinity);
    }
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
    { agent_type: 'builder', missing_requirements: 'TOKEN' }, { agent_type: 'builder', missing_requirements: [42] },
    ...['', 'token/name', 'a'.repeat(129), 42].map(name => ({
      agent_type: 'builder', missing_requirements: [{ kind: 'secret', name }],
    })),
    { missing_requirements: [{ kind: 'secret', name: 'TOKEN' }] },
  ])('rejects malformed managed secret recipient types: %j', async patch => {
    respond({ ...operation, preflight: { ready: false, version: 1, actions: [{ action_name: 'build',
      unavailable_agents: [{ agent_id: 'builder-agent-1234abcd', reason: 'Missing', ...patch }],
    }] } });

    await expect(getOperation(url, credentials, operation.id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it('accepts unavailable candidates without managed secret requirements', async () => {
    const body = { ...operation, preflight: { ready: false, version: 1, actions: [{ action_name: 'build', unavailable_agents: [
      { agent_id: 'agent-1', reason: 'Missing dependency' },
      { agent_id: 'agent-2', agent_type: 'builder', reason: 'Missing dependency', missing_requirements: null },
      { agent_id: 'agent-3', agent_type: 'builder', reason: 'Missing dependency', missing_requirements: [] },
      { agent_id: 'agent-4', reason: 'Missing local setting', missing_requirements: [{ kind: 'configuration', name: 'TOKEN', sensitive: true }] },
    ] }] } };
    respond(body);

    expect(await getOperation(url, credentials, operation.id)).toEqual(body);
  });

  it.each([null, {}, { items: null }, { items: [null] }])('rejects a malformed list envelope: %j', async body => {
    respond(body);

    await expect(listOperations(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
  });
});

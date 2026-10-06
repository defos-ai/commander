import { describe, expect, it, vi } from 'vitest';
import { getAction, listActions } from '../src/api/actions';
import { getAgent, listAgents } from '../src/api/agents';
import type { Action, Agent } from '../src/api/types';

const url = 'https://commander.example/team';
const credentials = { url, username: 'operator' };
const action: Action = {
  name: 'capture-page', description: 'Capture a page in the browser', status: 'unavailable',
  status_reason: 'Browser and API key are missing', required_permissions: ['network'],
  missing_requirements: [
    { kind: 'executable', name: 'chromium' },
    { kind: 'configuration', name: 'BROWSER_API_KEY', secret: true },
  ],
  input_schema: { type: 'object', properties: { url: { type: 'string' } } }, output_schema: {},
};
const agent: Agent = {
  id: 'web-quality-agent-0123abcd', type: 'web-quality', status: 'working', stale: false,
  server: { ip_address: '10.0.0.2', hostname: 'agent-pod-1' },
  work: { action_name: 'check-links', operation_id: 'operation-1', task_id: 'task-1', command_id: 'command-1' },
  responsibilities: ['Review websites'], granted_permissions: ['network'], actions: [action],
  discovery_scope: { paths: ['/usr/bin'], services: null }, config: { max_pages: 3 },
  observed_at: '2026-10-05T09:00:00.123456789Z', last_seen: '2026-10-05T09:00:01Z',
  last_heartbeat: '2026-10-05T09:00:00Z', lease_expires_at: '2026-10-05T09:00:45Z',
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

describe('agent and action catalogs', () => {
  it('keeps readiness, requirements, current work and timestamps in lists and details', async () => {
    const fetch = respond({ items: [agent] }, agent, { items: [action] }, action);

    expect(await listAgents(url, credentials)).toEqual([agent]);
    expect(await getAgent(url, credentials, agent.id)).toEqual(agent);
    expect(await listActions(url, credentials)).toEqual([action]);
    expect(await getAction(url, credentials, action.name)).toEqual(action);
    expect(fetch.mock.calls.map(([path]) => proxyTarget(path))).toEqual([
      { url, path: '/api/v1/agents' }, { url, path: `/api/v1/agents/${agent.id}` },
      { url, path: '/api/v1/actions' }, { url, path: `/api/v1/actions/${action.name}` },
    ]);
    for (const [, options] of fetch.mock.calls) {
      expect(options).toMatchObject({ method: 'GET', credentials: 'same-origin', headers: { 'X-Commander-UI': '1' } });
      expect(new Headers(options?.headers).has('Authorization')).toBe(false);
    }
  });

  it('accepts empty catalogs, nullable Go fields and unknown future status values', async () => {
    const nullableAction: Action = {
      name: 'future-action', description: '', status: 'preparing',
      required_permissions: null, missing_requirements: null, input_schema: null, output_schema: null,
    };
    const nullableAgent: Agent = {
      id: 'future-agent', type: 'future', status: 'draining', stale: true,
      server: { ip_address: '', hostname: '' }, work: null, responsibilities: null,
      granted_permissions: null, actions: null, discovery_scope: null, config: null,
      last_seen: '0001-01-01T00:00:00Z', last_heartbeat: '0001-01-01T00:00:00Z',
    };
    respond({ items: [] }, { items: [] }, nullableAgent, nullableAction);

    expect(await listAgents(url, credentials)).toEqual([]);
    expect(await listActions(url, credentials)).toEqual([]);
    expect(await getAgent(url, credentials, nullableAgent.id)).toEqual(nullableAgent);
    expect(await getAction(url, credentials, nullableAction.name)).toEqual(nullableAction);
  });

  it('accepts arbitrary JSON configuration and action requirement kinds', async () => {
    const extensionAction = { ...action, missing_requirements: [{ kind: 'future_requirement', name: 'extension' }] };
    const extensionAgent = { ...agent, config: ['extension', { enabled: true }], actions: [extensionAction] };
    respond(extensionAgent, extensionAction);

    expect(await getAgent(url, credentials, agent.id)).toEqual(extensionAgent);
    expect(await getAction(url, credentials, action.name)).toEqual(extensionAction);
  });

  it('encodes identifiers as one URL segment without losing the API base path', async () => {
    const id = 'team/agent ?#é';
    const name = 'review/links ?#é';
    const fetch = respond({ ...agent, id }, { ...action, name });

    await getAgent(`${url}/`, credentials, id);
    await getAction(`${url}/`, credentials, name);

    expect(fetch.mock.calls.map(([path]) => proxyTarget(path))).toEqual([
      { url, path: '/api/v1/agents/team%2Fagent%20%3F%23%C3%A9' },
      { url, path: '/api/v1/actions/review%2Flinks%20%3F%23%C3%A9' },
    ]);
  });

  it('rejects empty, dot and unencodable identifiers before making a request', async () => {
    const fetch = respond();

    for (const value of ['', ' ', '.', '..', '\ud800']) {
      await expect(getAgent(url, credentials, value)).rejects.toMatchObject({ kind: 'invalid_response' });
      await expect(getAction(url, credentials, value)).rejects.toMatchObject({ kind: 'invalid_response' });
    }

    expect(fetch).not.toHaveBeenCalled();
  });

  it('rejects details for a different identity', async () => {
    respond({ ...agent, id: 'different-agent' }, { ...action, name: 'different-action' });

    await expect(getAgent(url, credentials, agent.id)).rejects.toMatchObject({ kind: 'invalid_response' });
    await expect(getAction(url, credentials, action.name)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it.each([
    { server: null },
    { work: { action_name: 'check-links', task_id: 42 } },
    { actions: [{ ...action, missing_requirements: [{ kind: 'executable', name: 'chromium', secret: 'yes' }] }] },
    { responsibilities: 'review' },
    { discovery_scope: { paths: ['/usr/bin'], services: [42] } },
    { last_heartbeat: 'not a date' },
    { lease_expires_at: 42 },
  ])('rejects malformed agent fields before rendering: %j', async patch => {
    respond({ items: [{ ...agent, ...patch }] });

    await expect(listAgents(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it.each([
    { status_reason: { message: 'missing' } },
    { required_permissions: ['network', 42] },
    { missing_requirements: [{ name: 'chromium' }] },
    { input_schema: [] },
    { output_schema: true },
  ])('rejects malformed action fields before rendering: %j', async patch => {
    respond({ items: [{ ...action, ...patch }] });

    await expect(listActions(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it('requires list envelopes instead of accepting null or missing items', async () => {
    respond({ items: null }, {});

    await expect(listAgents(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
    await expect(listActions(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
  });
});

import { describe, expect, it } from 'vitest';
import type { OperationGraph } from '../src/api/graph-types';
import { getOperationGraph } from '../src/api/operation-graph';
import { respondJSON as respond } from './fakes/fetch';
import { credentials, graph, url } from './fixtures/operation-graph-client';

function proxyTarget(input: RequestInfo | URL) {
  const endpoint = new URL(String(input), window.location.origin);
  expect(endpoint.origin).toBe(window.location.origin);
  expect(endpoint.pathname).toBe('/ui/proxy');
  return Object.fromEntries(endpoint.searchParams);
}

describe('operation graph client', () => {
  it('loads the Go graph shape with authenticated GET and preserves all node metadata and edges', async () => {
    const fetch = respond(graph);

    const result = await getOperationGraph(`${url}/`, credentials, graph.operation_id);

    expect(result).toEqual(graph);
    expect(fetch).toHaveBeenCalledOnce();
    expect(proxyTarget(fetch.mock.calls[0][0])).toEqual({ url, path: '/api/v1/operations/operation-1/graph' });
  });

  it('accepts empty graphs and future operation status values', async () => {
    const empty = { ...graph, status: 'future-status', nodes: [], edges: [] };
    respond(empty);

    expect(await getOperationGraph(url, credentials, graph.operation_id)).toEqual(empty);
  });

  it('accepts queued tasks without agent or timestamps and nullable optional Go times', async () => {
    const queued: OperationGraph = { ...graph, nodes: [
      { id: 'task:task-1', kind: 'task', task: { id: 'task-1', action_name: 'check-links', status: 'queued' } },
      { id: 'task:task-2', kind: 'task', task: { id: 'task-2', action_name: 'capture-page', status: 'future-status', started_at: null, finished_at: null } },
    ], edges: [] };
    respond(queued);

    expect(await getOperationGraph(url, credentials, graph.operation_id)).toEqual(queued);
  });

  it('encodes operation IDs under the configured base URL', async () => {
    const operationID = 'team/operation ?#é';
    const fetch = respond({ ...graph, operation_id: operationID });

    await getOperationGraph(url, credentials, operationID);

    expect(proxyTarget(fetch.mock.calls[0][0])).toEqual({ url, path: '/api/v1/operations/team%2Foperation%20%3F%23%C3%A9/graph' });
  });

  it('rejects invalid IDs before sending and mismatched operation identity afterward', async () => {
    const fetch = respond({ ...graph, operation_id: 'other-operation' });

    for (const operationID of ['', ' ', '.', '..', '\ud800']) {
      await expect(getOperationGraph(url, credentials, operationID)).rejects.toMatchObject({ kind: 'invalid_response' });
    }
    expect(fetch).not.toHaveBeenCalled();
    await expect(getOperationGraph(url, credentials, graph.operation_id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it.each([
    { nodes: null },
    { edges: null },
    { nodes: [{ id: 'unknown:1', kind: 'future-node' }] },
    { nodes: [{ id: 'task:task-1', kind: 'task', task: null }] },
    { nodes: [{ id: 'task:task-1', kind: 'task', task: { id: 'task-1', action_name: 'check-links', status: 'queued', started_at: 'not a date' } }] },
    { nodes: [{ id: 'file:1', kind: 'file', file: { artifact_id: 'artifact-1', id: 'file-1', name: 'a.png', media_type: 'image/png', sha256: 'abc', size: -1 } }] },
    { nodes: [{ id: 'artifact:1', kind: 'artifact', artifact: { id: 'artifact-1', name: 'result', agent_id: 'agent-1', action_name: 'check-links' } }] },
    { edges: [{ source: 'task:task-1', target: 'task:task-2', kind: 'future-edge' }] },
    { updated_at: 'not a date' },
  ])('rejects unsafe or unsupported graph shape: %j', async patch => {
    respond({ ...graph, ...patch });

    await expect(getOperationGraph(url, credentials, graph.operation_id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it.each([
    { nodes: [...graph.nodes, graph.nodes[0]] },
    { edges: [...graph.edges, graph.edges[0]] },
    { edges: [{ source: 'task:missing', target: 'task:task-2', kind: 'parent_child' }] },
    { edges: [{ source: 'task:task-1', target: 'task:task-1', kind: 'parent_child' }] },
    { edges: [{ source: 'task:task-1', target: 'artifact:artifact-1', kind: 'parent_child' }] },
    { edges: [{ source: 'artifact:artifact-1', target: 'task:task-1', kind: 'produced' }] },
    { edges: [{ source: 'task:task-1', target: 'file:artifact-1:file-1', kind: 'contains' }] },
    { nodes: graph.nodes.map(node => node.kind === 'file' ? { ...node, file: { ...node.file, artifact_id: 'different-artifact' } } : node) },
  ])('rejects ambiguous or inconsistent graph connections: %j', async patch => {
    respond({ ...graph, ...patch });

    await expect(getOperationGraph(url, credentials, graph.operation_id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });
});

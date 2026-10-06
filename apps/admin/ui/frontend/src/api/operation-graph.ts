import type { Credentials } from '../connections/types';
import { ApiError, getJSON } from './client';
import type { GraphArtifact, GraphEdge, GraphFile, GraphNode, GraphTask, OperationGraph } from './graph-types';
import { encodeIdentifier, isIdentifier, isObject, isOptionalString } from './validation';

function isTimestamp(value: unknown): value is string {
  return typeof value === 'string' && Number.isFinite(Date.parse(value));
}

function isOptionalTimestamp(value: unknown): value is string | null | undefined {
  return value === undefined || value === null || isTimestamp(value);
}

function isTask(value: unknown): value is GraphTask {
  return isObject(value) && isIdentifier(value.id) && typeof value.action_name === 'string' &&
    typeof value.status === 'string' && isOptionalString(value.parent_task_id) && isOptionalString(value.step_id) &&
    isOptionalString(value.agent_id) && isOptionalString(value.command_id) && isOptionalString(value.error) &&
    isOptionalTimestamp(value.started_at) && isOptionalTimestamp(value.finished_at);
}

function isArtifact(value: unknown): value is GraphArtifact {
  return isObject(value) && isIdentifier(value.id) && typeof value.name === 'string' &&
    typeof value.agent_id === 'string' && typeof value.action_name === 'string' &&
    isOptionalString(value.command_id) && isTimestamp(value.created_at);
}

function isFile(value: unknown): value is GraphFile {
  return isObject(value) && isIdentifier(value.id) && isIdentifier(value.artifact_id) &&
    typeof value.name === 'string' && typeof value.media_type === 'string' && typeof value.sha256 === 'string' &&
    typeof value.size === 'number' && Number.isSafeInteger(value.size) && value.size >= 0;
}

function isNode(value: unknown): value is GraphNode {
  if (!isObject(value) || !isIdentifier(value.id)) return false;
  switch (value.kind) {
    case 'task': return isTask(value.task);
    case 'artifact': return isArtifact(value.artifact);
    case 'file': return isFile(value.file);
    default: return false;
  }
}

function isEdge(value: unknown): value is GraphEdge {
  return isObject(value) && isIdentifier(value.source) && isIdentifier(value.target) &&
    (value.kind === 'parent_child' || value.kind === 'produced' || value.kind === 'contains');
}

function validConnections(nodes: GraphNode[], edges: GraphEdge[]): boolean {
  const byID = new Map(nodes.map(node => [node.id, node]));
  if (byID.size !== nodes.length) return false;
  const seen = new Set<string>();
  return edges.every(edge => {
    const source = byID.get(edge.source);
    const target = byID.get(edge.target);
    const key = JSON.stringify([edge.kind, edge.source, edge.target]);
    if (!source || !target || source.id === target.id || seen.has(key)) return false;
    seen.add(key);
    switch (edge.kind) {
      case 'parent_child': return source.kind === 'task' && target.kind === 'task';
      case 'produced': return source.kind === 'task' && target.kind === 'artifact';
      case 'contains': return source.kind === 'artifact' && target.kind === 'file' && source.artifact.id === target.file.artifact_id;
    }
  });
}

function isGraph(value: unknown): value is OperationGraph {
  return isObject(value) && isIdentifier(value.operation_id) && typeof value.status === 'string' &&
    isTimestamp(value.updated_at) && Array.isArray(value.nodes) && value.nodes.every(isNode) &&
    Array.isArray(value.edges) && value.edges.every(isEdge) && validConnections(value.nodes, value.edges);
}

export async function getOperationGraph(url: string, credentials: Credentials, operationID: string, signal?: AbortSignal): Promise<OperationGraph> {
  const body = await getJSON(url, `/api/v1/operations/${encodeIdentifier(operationID)}/graph`, credentials, signal);
  if (!isGraph(body) || body.operation_id !== operationID) throw new ApiError('invalid_response');
  return body;
}

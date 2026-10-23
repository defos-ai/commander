import type { Session } from '../connections/types';
import { isAction, isActionRequirement } from './actions';
import { ApiError, getJSON } from './client';
import type { Agent, AgentWork } from './types';
import { encodeIdentifier, isIdentifier, isNullableStringArray, isObject, isOptionalString } from './validation';

function isWork(value: unknown): value is AgentWork {
  return isObject(value) && typeof value.action_name === 'string' &&
    isOptionalString(value.operation_id) && isOptionalString(value.task_id) &&
    isOptionalString(value.step_id) && isOptionalString(value.command_id) &&
    (value.missing_requirements === undefined || value.missing_requirements === null ||
      (Array.isArray(value.missing_requirements) && value.missing_requirements.every(isActionRequirement)));
}

function isTimestamp(value: unknown): value is string {
  return typeof value === 'string' && Number.isFinite(Date.parse(value));
}

function isAgent(value: unknown): value is Agent {
  return isObject(value) && isIdentifier(value.id) && typeof value.type === 'string' &&
    typeof value.status === 'string' && typeof value.stale === 'boolean' &&
    (value.work === undefined || value.work === null || isWork(value.work)) &&
    isObject(value.server) && typeof value.server.ip_address === 'string' && typeof value.server.hostname === 'string' &&
    isNullableStringArray(value.responsibilities) &&
    (value.actions === null || (Array.isArray(value.actions) && value.actions.every(isAction))) &&
    (value.discovery_scope === null || (isObject(value.discovery_scope) &&
      isNullableStringArray(value.discovery_scope.paths) && isNullableStringArray(value.discovery_scope.services))) &&
    'config' in value && isTimestamp(value.last_seen) && isTimestamp(value.last_heartbeat) &&
    (value.observed_at === undefined || isTimestamp(value.observed_at)) &&
    (value.lease_expires_at === undefined || isTimestamp(value.lease_expires_at));
}

export async function listAgents(url: string, credentials: Session, signal?: AbortSignal): Promise<Agent[]> {
  const body = await getJSON(url, '/api/v1/agents', credentials, signal);
  if (!isObject(body) || !Array.isArray(body.items) || !body.items.every(isAgent)) {
    throw new ApiError('invalid_response');
  }
  return body.items;
}

export async function getAgent(url: string, credentials: Session, id: string, signal?: AbortSignal): Promise<Agent> {
  const body = await getJSON(url, `/api/v1/agents/${encodeIdentifier(id)}`, credentials, signal);
  if (!isAgent(body) || body.id !== id) throw new ApiError('invalid_response');
  return body;
}

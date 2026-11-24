import { expireSession, proxyURL } from '../connections/client';
import type { Session } from '../connections/types';
import { isAction, isActionRequirement } from './actions';
import { ApiError, getJSON } from './client';
import type { Agent, AgentDependency, AgentSelector, AgentWork } from './types';
import { encodeIdentifier, isIdentifier, isNullableStringArray, isObject, isOptionalString } from './validation';

export type AgentStopErrorKind = 'authentication' | 'forbidden' | 'invalid_input' | 'unavailable' | 'unknown';
export type AgentStopFailureCode = 'agent_not_found' | 'agent_stop_uncertain' | 'agent_stop_not_requested';

export interface AgentStopResult {
  items: Agent[];
  errors?: { id: string; error: AgentStopFailureCode }[];
}

export class AgentStopError extends Error {
  constructor(readonly kind: AgentStopErrorKind) {
    super(kind);
    this.name = 'AgentStopError';
  }
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const agentID = /^[a-z0-9]+(?:-[a-z0-9]+)*-agent-[0-9a-f]{8}$/;
const agentType = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

function isAgentID(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 63 && agentID.test(value);
}

export function validAgentSelector(selector: unknown): selector is AgentSelector {
  if (!isObject(selector) || !Object.values(selector).some(value => value !== undefined) ||
    Object.keys(selector).some(name => name !== 'id' && name !== 'type' && name !== 'version' && name !== 'hostname')) return false;
  return (selector.id === undefined || isAgentID(selector.id)) &&
    (selector.type === undefined || typeof selector.type === 'string' && selector.type.length <= 63 && agentType.test(selector.type)) &&
    (selector.version === undefined || typeof selector.version === 'string' && selector.version !== '' &&
      new TextEncoder().encode(selector.version).length <= 128 && !/[\p{White_Space}\p{Cc}]/u.test(selector.version)) &&
    (selector.hostname === undefined || typeof selector.hostname === 'string' && selector.hostname !== '' &&
      !/[\p{White_Space}\p{Cc}/\\]/u.test(selector.hostname));
}

function isWork(value: unknown): value is AgentWork {
  return isObject(value) && typeof value.action_name === 'string' &&
    isOptionalString(value.operation_id) && isOptionalString(value.task_id) &&
    isOptionalString(value.command_id) &&
    (value.missing_requirements === undefined || value.missing_requirements === null ||
      (Array.isArray(value.missing_requirements) && value.missing_requirements.every(isActionRequirement)));
}

function isTimestamp(value: unknown): value is string {
  return typeof value === 'string' && Number.isFinite(Date.parse(value));
}

function isDependency(value: unknown): value is AgentDependency {
  return isObject(value) && typeof value.name === 'string' && typeof value.description === 'string' &&
    isOptionalString(value.version);
}

function validStopMetadata(value: Record<string, unknown>): boolean {
  if (value.status !== 'stop_requested' && value.stop_command_id === undefined && value.stop_requested_at === undefined) return true;
  return typeof value.stop_command_id === 'string' && uuid.test(value.stop_command_id) &&
    isTimestamp(value.stop_requested_at) && !value.stop_requested_at.startsWith('0001-') &&
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(value.stop_requested_at);
}

function isAgent(value: unknown): value is Agent {
  return isObject(value) && isIdentifier(value.id) && typeof value.type === 'string' &&
    isOptionalString(value.version) &&
    typeof value.status === 'string' && typeof value.stale === 'boolean' &&
    validStopMetadata(value) &&
    (value.work === undefined || value.work === null || isWork(value.work)) &&
    isObject(value.server) && typeof value.server.ip_address === 'string' && typeof value.server.hostname === 'string' &&
    isNullableStringArray(value.responsibilities) &&
    (value.dependencies === undefined || (Array.isArray(value.dependencies) && value.dependencies.every(isDependency))) &&
    (value.actions === null || (Array.isArray(value.actions) && value.actions.every(isAction))) &&
    (value.discovery_scope === null || (isObject(value.discovery_scope) &&
      isNullableStringArray(value.discovery_scope.paths) && isNullableStringArray(value.discovery_scope.services))) &&
    'config' in value && isTimestamp(value.last_seen) && isTimestamp(value.last_heartbeat) &&
    (value.observed_at === undefined || isTimestamp(value.observed_at)) &&
    (value.lease_expires_at === undefined || isTimestamp(value.lease_expires_at));
}

export async function listAgents(url: string, credentials: Session, signal?: AbortSignal): Promise<Agent[]> {
  const body = await getJSON(url, '/api/v1/agents', credentials, signal);
  if (!isObject(body) || !Array.isArray(body.items) || !body.items.every(isAgent) || new Set(body.items.map(item => item.id)).size !== body.items.length) {
    throw new ApiError('invalid_response');
  }
  return body.items;
}

export async function getAgent(url: string, credentials: Session, id: string, signal?: AbortSignal): Promise<Agent> {
  const body = await getJSON(url, `/api/v1/agents/${encodeIdentifier(id)}`, credentials, signal);
  if (!isAgent(body) || body.id !== id) throw new ApiError('invalid_response');
  return body;
}

export function matchesAgentSelector(agent: Agent, selector: AgentSelector): boolean {
  return (selector.id === undefined || agent.id === selector.id) &&
    (selector.type === undefined || agent.type === selector.type) &&
    (selector.version === undefined || agent.version === selector.version) &&
    (selector.hostname === undefined || agent.server.hostname === selector.hostname);
}

function readStopResult(value: unknown, selector: AgentSelector, status: number): AgentStopResult {
  if (!isObject(value) || !Array.isArray(value.items) || !value.items.every(item => isAgent(item) && isAgentID(item.id) &&
    (item.status === 'stop_requested' || item.status === 'stopped') && matchesAgentSelector(item, selector)) ||
    value.errors !== undefined && !Array.isArray(value.errors)) throw new AgentStopError('unknown');
  const items = value.items as Agent[];
  const errors = (value.errors ?? []) as unknown[];
  const seen = new Set(items.map(item => item.id));
  if (seen.size !== items.length || (items.some(item => item.status === 'stop_requested') ? status !== 202 : status !== 200)) {
    throw new AgentStopError('unknown');
  }
  for (const failure of errors) {
    if (!isObject(failure) || !isAgentID(failure.id) || seen.has(failure.id) || selector.id !== undefined && failure.id !== selector.id ||
      (failure.error !== 'agent_not_found' && failure.error !== 'agent_stop_uncertain' && failure.error !== 'agent_stop_not_requested')) {
      throw new AgentStopError('unknown');
    }
    seen.add(failure.id);
  }
  return { items, ...(value.errors !== undefined ? { errors: errors as NonNullable<AgentStopResult['errors']> } : {}) };
}

function stopResponseError(status: number, body: unknown): AgentStopError {
  return new AgentStopError(status === 401 ? 'authentication' : status === 403 ? 'forbidden'
    : status === 400 || status === 413 || status === 415 || status === 422 ? 'invalid_input'
      : status === 503 && isObject(body) && body.error === 'agent_controls_unavailable' ? 'unavailable' : 'unknown');
}

export async function stopAgents(url: string, credentials: Session, selector: AgentSelector, signal?: AbortSignal): Promise<AgentStopResult> {
  if (!validAgentSelector(selector)) throw new AgentStopError('invalid_input');
  selector = { ...selector };
  if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
  let endpoint: string;
  try { endpoint = proxyURL(url, '/api/v1/agents/stop', credentials); }
  catch { throw new AgentStopError('invalid_input'); }
  const controller = new AbortController();
  const abort = () => controller.abort();
  let rejectAborted = () => {};
  const aborted = new Promise<never>((_, reject) => {
    rejectAborted = () => reject(new AgentStopError('unknown'));
    controller.signal.addEventListener('abort', rejectAborted, { once: true });
  });
  signal?.addEventListener('abort', abort, { once: true });
  const timeout = setTimeout(abort, 30_000);
  const body = JSON.stringify({ selector });

  async function send() {
    const response = await fetch(endpoint, {
      method: 'POST', headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-Commander-UI': '1' },
      credentials: 'same-origin', redirect: 'error', cache: 'no-store', signal: controller.signal, body,
    });
    controller.signal.throwIfAborted();
    if (response.status === 401) expireSession(url);
    let result: unknown;
    try { result = await response.json(); }
    catch { if (response.ok) throw new AgentStopError('unknown'); }
    controller.signal.throwIfAborted();
    if (!response.ok) throw stopResponseError(response.status, result);
    if (response.status !== 200 && response.status !== 202) throw new AgentStopError('unknown');
    return readStopResult(result, selector, response.status);
  }

  try { return await Promise.race([send(), aborted]); }
  catch (error) {
    if (!controller.signal.aborted && error instanceof AgentStopError) throw error;
    throw new AgentStopError('unknown');
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener('abort', abort);
    controller.signal.removeEventListener('abort', rejectAborted);
  }
}

import { expireSession, proxyURL } from '../connections/client';
import type { Session } from '../connections/types';
import { ApiError } from './client';
import { isObject } from './validation';

export type SessionControlMethod = 'inspect' | 'verify' | 'close' | 'abandon';
export type SessionControlErrorKind = 'authentication' | 'forbidden' | 'not_found' | 'invalid_input' | 'conflict' | 'unavailable' | 'unknown';

export class SessionControlError extends Error {
  constructor(readonly kind: SessionControlErrorKind, readonly requestID: string) {
    super(kind);
    this.name = 'SessionControlError';
  }
}

export interface RuntimeSession {
  id: string;
  scope_id: string;
  owner_agent_id: string;
  kind: string;
  label: string;
  state: 'starting' | 'listening' | 'verifying' | 'connected' | 'unverified' | 'lost' | 'closed' | 'failed';
  disposition: 'reserved' | 'active' | 'closing' | 'released';
  release_reason?: '' | 'closed' | 'abandoned' | 'cancelled_before_effect';
  endpoint?: string;
  peer?: string;
  hostname?: string;
  user?: string;
  reason?: string;
  busy: boolean;
  revision: number;
  created_at: string;
  updated_at: string;
  connected_at?: string;
  verified_at?: string;
}

export interface SessionFilters {
  scope_id?: string;
  owner_agent_id?: string;
}

const sessionsPath = '/api/v1/sessions';
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const agentIDPattern = /^[a-z0-9]+(-[a-z0-9]+)*-agent-[0-9a-f]{8}$/;
const states = ['starting', 'listening', 'verifying', 'connected', 'unverified', 'lost', 'closed', 'failed'];
const dispositions = ['reserved', 'active', 'closing', 'released'];
const releaseReasons = ['', 'closed', 'abandoned', 'cancelled_before_effect'];
const methods: readonly string[] = ['inspect', 'verify', 'close', 'abandon'];

function isUUID(value: unknown): value is string {
  return typeof value === 'string' && uuidPattern.test(value);
}

function isAgentID(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 63 && agentIDPattern.test(value);
}

function isTimestamp(value: unknown): value is string {
  if (typeof value !== 'string') return false;
  const parts = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|[+-](\d{2}):(\d{2}))$/.exec(value);
  if (!parts) return false;
  const [year, month, day, hour, minute, second, offsetHour, offsetMinute] = parts.slice(1).map(part => Number(part ?? 0));
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return year >= 1 && month >= 1 && month <= 12 && day >= 1 && day <= days[month - 1] &&
    hour <= 23 && minute <= 59 && second <= 59 && offsetHour <= 23 && offsetMinute <= 59 &&
    Number.isFinite(Date.parse(value)) && Date.parse(value) !== Date.parse('0001-01-01T00:00:00Z');
}

function readSession(value: unknown): RuntimeSession {
  if (!isObject(value) || !isUUID(value.id) || !isUUID(value.scope_id) ||
    (value.owner_agent_id !== '' && !isAgentID(value.owner_agent_id)) ||
    typeof value.kind !== 'string' || value.kind === '' || typeof value.label !== 'string' || value.label === '' ||
    typeof value.state !== 'string' || !states.includes(value.state) ||
    typeof value.disposition !== 'string' || !dispositions.includes(value.disposition) ||
    (value.release_reason !== undefined && (typeof value.release_reason !== 'string' || !releaseReasons.includes(value.release_reason))) ||
    typeof value.busy !== 'boolean' || typeof value.revision !== 'number' || !Number.isSafeInteger(value.revision) || value.revision <= 0 ||
    !isTimestamp(value.created_at) || !isTimestamp(value.updated_at)) throw new ApiError('invalid_response');
  const item: RuntimeSession = {
    id: value.id, scope_id: value.scope_id, owner_agent_id: value.owner_agent_id as string,
    kind: value.kind, label: value.label, state: value.state as RuntimeSession['state'],
    disposition: value.disposition as RuntimeSession['disposition'], busy: value.busy, revision: value.revision,
    created_at: value.created_at, updated_at: value.updated_at,
  };
  if (value.release_reason !== undefined) item.release_reason = value.release_reason as RuntimeSession['release_reason'];
  for (const name of ['endpoint', 'peer', 'hostname', 'user', 'reason'] as const) {
    if (value[name] !== undefined) {
      if (typeof value[name] !== 'string') throw new ApiError('invalid_response');
      item[name] = value[name];
    }
  }
  for (const name of ['connected_at', 'verified_at'] as const) {
    if (value[name] !== undefined) {
      if (!isTimestamp(value[name])) throw new ApiError('invalid_response');
      item[name] = value[name];
    }
  }
  return item;
}

function responseError(status: number, body: unknown, requestID: string | undefined): Error {
  if (requestID !== undefined) {
    const kind: SessionControlErrorKind = status === 401 ? 'authentication'
      : status === 403 ? 'forbidden'
        : status === 404 ? 'not_found'
          : status === 409 || status === 422 ? 'conflict'
            : status === 400 || status === 413 || status === 415 ? 'invalid_input'
              : status === 503 && isObject(body) && body.error === 'sessions_unavailable' ? 'unavailable' : 'unknown';
    return new SessionControlError(kind, requestID);
  }
  return new ApiError(status === 401 ? 'authentication' : status === 403 ? 'forbidden'
    : status === 404 ? 'not_found' : status === 408 || status === 504 ? 'timeout' : 'unavailable');
}

async function request<T>(
  url: string, credentials: Session, path: string, read: (body: unknown) => T,
  signal?: AbortSignal, control?: { requestID: string; body: unknown },
): Promise<T> {
  if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
  let endpoint: string;
  try { endpoint = proxyURL(url, path, credentials); }
  catch { throw control ? new SessionControlError('invalid_input', control.requestID) : new ApiError('invalid_response'); }
  const controller = new AbortController();
  const abort = () => controller.abort();
  let rejectAborted = () => {};
  const aborted = new Promise<never>((_, reject) => {
    rejectAborted = () => reject(control ? new SessionControlError('unknown', control.requestID)
      : signal?.aborted ? new DOMException('Request aborted', 'AbortError') : new ApiError('timeout'));
    controller.signal.addEventListener('abort', rejectAborted, { once: true });
  });
  signal?.addEventListener('abort', abort, { once: true });
  const timeout = setTimeout(abort, control ? 30_000 : 5_000);

  async function send() {
    const response = await fetch(endpoint, {
      method: control ? 'POST' : 'GET',
      headers: { Accept: 'application/json', 'X-Commander-UI': '1', ...(control ? { 'Content-Type': 'application/json' } : {}) },
      credentials: 'same-origin', redirect: 'error', cache: 'no-store',
      signal: controller.signal, ...(control ? { body: JSON.stringify(control.body) } : {}),
    });
    controller.signal.throwIfAborted();
    if (response.status === 401) expireSession(url);
    let body: unknown;
    try { body = await response.json(); }
    catch { if (response.ok) throw new ApiError('invalid_response'); }
    controller.signal.throwIfAborted();
    if (!response.ok) throw responseError(response.status, body, control?.requestID);
    if (response.status !== 200) throw new ApiError('invalid_response');
    return read(body);
  }

  try {
    return await Promise.race([send(), aborted]);
  } catch (error) {
    if (control) {
      if (!controller.signal.aborted && error instanceof SessionControlError) throw error;
      throw new SessionControlError('unknown', control.requestID);
    }
    if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
    if (controller.signal.aborted) throw new ApiError('timeout');
    if (error instanceof ApiError) throw error;
    throw new ApiError('unavailable');
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener('abort', abort);
    controller.signal.removeEventListener('abort', rejectAborted);
  }
}

export async function listRuntimeSessions(url: string, credentials: Session, filters: SessionFilters = {}, signal?: AbortSignal): Promise<RuntimeSession[]> {
  if (filters.scope_id && !isUUID(filters.scope_id) || filters.owner_agent_id && !isAgentID(filters.owner_agent_id)) throw new ApiError('invalid_response');
  const query = new URLSearchParams();
  if (filters.scope_id) query.set('scope_id', filters.scope_id);
  if (filters.owner_agent_id) query.set('owner_agent_id', filters.owner_agent_id);
  const path = query.size ? `${sessionsPath}?${query}` : sessionsPath;
  return request(url, credentials, path, body => {
    if (!isObject(body) || !Array.isArray(body.items)) throw new ApiError('invalid_response');
    const items = body.items.map(readSession);
    if (new Set(items.map(item => item.id)).size !== items.length || items.some(item =>
      filters.scope_id && item.scope_id !== filters.scope_id || filters.owner_agent_id && item.owner_agent_id !== filters.owner_agent_id)) {
      throw new ApiError('invalid_response');
    }
    return items;
  }, signal);
}

export async function getRuntimeSession(url: string, credentials: Session, id: string, signal?: AbortSignal): Promise<RuntimeSession> {
  if (!isUUID(id)) throw new ApiError('invalid_response');
  return request(url, credentials, `${sessionsPath}/${id}`, body => {
    const item = readSession(body);
    if (item.id !== id) throw new ApiError('invalid_response');
    return item;
  }, signal);
}

export async function controlRuntimeSession(
  url: string, credentials: Session, session: Pick<RuntimeSession, 'id' | 'scope_id' | 'owner_agent_id'>,
  method: SessionControlMethod, requestID: string, signal?: AbortSignal,
): Promise<RuntimeSession> {
  if (!isUUID(session.id) || !isUUID(session.scope_id) || !isAgentID(session.owner_agent_id) ||
    !methods.includes(method) || !isUUID(requestID)) throw new SessionControlError('invalid_input', requestID);
  const body = { id: session.id, scope_id: session.scope_id, owner_agent_id: session.owner_agent_id, method, request_id: requestID };
  return request(url, credentials, `${sessionsPath}/control`, result => {
    const item = readSession(result);
    if (item.id !== session.id || item.scope_id !== session.scope_id || item.owner_agent_id !== session.owner_agent_id) throw new ApiError('invalid_response');
    return item;
  }, signal, { requestID, body });
}

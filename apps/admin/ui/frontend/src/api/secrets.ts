import { expireSession, proxyURL } from '../connections/client';
import type { Session } from '../connections/types';
import type { ApiErrorKind } from './client';
import { isObject } from './validation';

export interface SecretKey {
  name: string;
}

export interface SecretMetadata extends SecretKey {
  id: string;
  version: number;
  created_at: string;
  updated_at: string;
  expires_at?: string;
  status: 'available' | 'expired';
}

export interface PendingSecretRequest extends SecretKey {
  agent_type: string;
  id: string;
  agent_id: string;
  requested_at: string;
  expires_at: string;
  status: 'needs_value' | 'awaiting_delivery' | 'awaiting_installation';
}

export interface CreateSecretInput extends SecretKey {
  value: string;
  expires_at?: string;
}

export interface ReplaceSecretInput {
  id: string;
  version: number;
  value: string;
  expires_at?: string;
}

export type SecretErrorKind = ApiErrorKind | 'disabled' | 'invalid_input' | 'conflict' | 'unknown';

export class SecretError extends Error {
  constructor(readonly kind: SecretErrorKind) {
    super(kind);
    this.name = 'SecretError';
  }
}

const secretsPath = '/api/v1/secrets';
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const agentTypePattern = /^[a-z0-9]+(-[a-z0-9]+)*$/;
const secretNamePattern = /^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/;
const agentIDPattern = /^[a-z0-9]+(-[a-z0-9]+)*-agent-[0-9a-f]{8}$/;

function isAgentType(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 63 && agentTypePattern.test(value);
}

function isUUID(value: unknown): value is string {
  return typeof value === 'string' && uuidPattern.test(value);
}

function isKey(value: unknown): value is SecretKey {
  return isObject(value) && typeof value.name === 'string' && secretNamePattern.test(value.name);
}

function isVersion(value: unknown): value is number {
  // Larger uint64 values cannot be safely used in optimistic-concurrency writes.
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
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
    Number.isFinite(Date.parse(value));
}

function isOptionalTimestamp(value: unknown): value is string | undefined {
  return value === undefined || isTimestamp(value);
}

function readMetadata(value: unknown): SecretMetadata {
  if (!isObject(value) || !isKey(value) || 'agent_type' in value || !isUUID(value.id) || !isVersion(value.version) ||
    !isTimestamp(value.created_at) || !isTimestamp(value.updated_at) || !isOptionalTimestamp(value.expires_at) ||
    (value.status !== 'available' && value.status !== 'expired')) throw new SecretError('invalid_response');
  return {
    id: value.id, name: value.name, version: value.version,
    created_at: value.created_at, updated_at: value.updated_at, status: value.status,
    ...(value.expires_at === undefined ? {} : { expires_at: value.expires_at }),
  };
}

function readPendingRequest(value: unknown): PendingSecretRequest {
  if (!isObject(value) || !isKey(value) || !isAgentType(value.agent_type) || !isUUID(value.id) || typeof value.agent_id !== 'string' ||
    value.agent_id.length > 63 || !agentIDPattern.test(value.agent_id) ||
    !isTimestamp(value.requested_at) || !isTimestamp(value.expires_at) ||
    (value.status !== 'needs_value' && value.status !== 'awaiting_delivery' && value.status !== 'awaiting_installation')) {
    throw new SecretError('invalid_response');
  }
  return {
    id: value.id, agent_type: value.agent_type, name: value.name, agent_id: value.agent_id,
    requested_at: value.requested_at, expires_at: value.expires_at, status: value.status,
  };
}

function readList<T extends { id: string }>(body: unknown, read: (value: unknown) => T): T[] {
  if (!isObject(body) || !Array.isArray(body.items)) throw new SecretError('invalid_response');
  const items = body.items.map(read);
  if (new Set(items.map(item => item.id)).size !== items.length) throw new SecretError('invalid_response');
  return items;
}

function readMatchingMetadata(body: unknown, key: SecretKey): SecretMetadata {
  const metadata = readMetadata(body);
  if (metadata.name !== key.name) throw new SecretError('invalid_response');
  return metadata;
}

function keyPath(key: SecretKey): string {
  if (!isKey(key)) throw new SecretError('invalid_input');
  return `${secretsPath}/${encodeURIComponent(key.name)}`;
}

function responseError(status: number, body: unknown, mutation: boolean): SecretError {
  const code = isObject(body) ? body.error : undefined;
  if (status === 503 && code === 'secrets_unavailable') return new SecretError('disabled');
  if (mutation && (status >= 500 || status === 408 || code === 'secret_write_uncertain')) return new SecretError('unknown');
  const kind: SecretErrorKind = status === 401 ? 'authentication'
    : status === 403 ? 'forbidden'
      : status === 404 ? 'not_found'
        : status === 409 ? 'conflict'
          : [400, 413, 415, 422].includes(status) ? 'invalid_input'
            : status === 408 || status === 504 ? 'timeout' : mutation ? 'unknown' : 'unavailable';
  return new SecretError(kind);
}

async function request<T>(
  url: string, credentials: Session, path: string, method: 'GET' | 'POST' | 'PUT' | 'DELETE',
  expectedStatus: number, read: (body: unknown) => T, body?: unknown, signal?: AbortSignal,
): Promise<T> {
  if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
  let endpoint: string;
  let encodedBody: string | undefined;
  try {
    endpoint = proxyURL(url, path, credentials);
    encodedBody = body === undefined ? undefined : JSON.stringify(body);
  } catch { throw new SecretError('invalid_input'); }

  const mutation = method !== 'GET';
  const controller = new AbortController();
  const abort = () => controller.abort();
  let timedOut = false;
  let rejectAborted = () => {};
  const aborted = new Promise<never>((_, reject) => {
    rejectAborted = () => reject(mutation ? new SecretError('unknown')
      : timedOut ? new SecretError('timeout') : new DOMException('Request aborted', 'AbortError'));
    controller.signal.addEventListener('abort', rejectAborted, { once: true });
  });
  signal?.addEventListener('abort', abort, { once: true });
  const timeout = setTimeout(() => { timedOut = true; abort(); }, mutation ? 30_000 : 5_000);

  async function send() {
    const response = await fetch(endpoint, {
      method, headers: {
        Accept: 'application/json', 'X-Commander-UI': '1',
        ...(encodedBody === undefined ? {} : { 'Content-Type': 'application/json' }),
      },
      credentials: 'same-origin', redirect: 'error', cache: 'no-store', body: encodedBody, signal: controller.signal,
    });
    controller.signal.throwIfAborted();
    if (response.status === 401) expireSession(url);
    let result: unknown;
    if (response.status !== 204) {
      try { result = await response.json(); }
      catch { if (response.ok) throw new SecretError('invalid_response'); }
    }
    controller.signal.throwIfAborted();
    if (!response.ok) throw responseError(response.status, result, mutation);
    if (response.status !== expectedStatus) throw new SecretError('invalid_response');
    return read(result);
  }

  try {
    return await Promise.race([send(), aborted]);
  } catch (error) {
    if (controller.signal.aborted) {
      if (mutation) throw new SecretError('unknown');
      if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
      throw new SecretError('timeout');
    }
    if (error instanceof SecretError) {
      if (mutation && error.kind === 'invalid_response') throw new SecretError('unknown');
      throw error;
    }
    throw new SecretError(mutation ? 'unknown' : 'unavailable');
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener('abort', abort);
    controller.signal.removeEventListener('abort', rejectAborted);
  }
}

export async function listSecrets(url: string, credentials: Session, signal?: AbortSignal): Promise<SecretMetadata[]> {
  return request(url, credentials, secretsPath, 'GET', 200, body => {
    const items = readList(body, readMetadata);
    if (new Set(items.map(item => item.name)).size !== items.length) throw new SecretError('invalid_response');
    return items;
  }, undefined, signal);
}

export async function listSecretRequests(url: string, credentials: Session, signal?: AbortSignal): Promise<PendingSecretRequest[]> {
  return request(url, credentials, '/api/v1/secret-requests', 'GET', 200, body => readList(body, readPendingRequest), undefined, signal);
}

export async function getSecret(url: string, credentials: Session, key: SecretKey, signal?: AbortSignal): Promise<SecretMetadata> {
  return request(url, credentials, keyPath(key), 'GET', 200, body => readMatchingMetadata(body, key), undefined, signal);
}

export async function createSecret(url: string, credentials: Session, input: CreateSecretInput, signal?: AbortSignal): Promise<SecretMetadata> {
  if (!isKey(input) || typeof input.value !== 'string' || !isOptionalTimestamp(input.expires_at)) throw new SecretError('invalid_input');
  const body = { name: input.name, value: input.value, expires_at: input.expires_at };
  return request(url, credentials, secretsPath, 'POST', 201, result => {
    const metadata = readMatchingMetadata(result, input);
    if (metadata.version !== 1) throw new SecretError('invalid_response');
    return metadata;
  }, body, signal);
}

export async function replaceSecret(url: string, credentials: Session, key: SecretKey, input: ReplaceSecretInput, signal?: AbortSignal): Promise<SecretMetadata> {
  if (!isUUID(input.id) || !isVersion(input.version) || !Number.isSafeInteger(input.version + 1) ||
    typeof input.value !== 'string' || !isOptionalTimestamp(input.expires_at)) throw new SecretError('invalid_input');
  const body = { id: input.id, version: input.version, value: input.value, expires_at: input.expires_at };
  return request(url, credentials, keyPath(key), 'PUT', 200, result => {
    const metadata = readMatchingMetadata(result, key);
    if (metadata.id !== input.id || metadata.version !== input.version + 1) throw new SecretError('invalid_response');
    return metadata;
  }, body, signal);
}

export async function deleteSecret(url: string, credentials: Session, metadata: SecretMetadata, signal?: AbortSignal): Promise<void> {
  if (!isUUID(metadata.id) || !isVersion(metadata.version)) throw new SecretError('invalid_input');
  const path = `${keyPath(metadata)}?${new URLSearchParams({ id: metadata.id, version: String(metadata.version) })}`;
  return request(url, credentials, path, 'DELETE', 204, () => undefined, undefined, signal);
}

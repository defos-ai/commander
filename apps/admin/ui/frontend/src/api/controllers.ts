import type { Session } from '../connections/types';
import { ApiError, getJSON } from './client';
import type { Controller } from './types';
import { encodeIdentifier, isIdentifier, isObject, isOptionalString } from './validation';

function isTimestamp(value: unknown): value is string {
  return typeof value === 'string' && Number.isFinite(Date.parse(value));
}

function isController(value: unknown): value is Controller {
  return isObject(value) && isIdentifier(value.id) && typeof value.type === 'string' &&
    typeof value.status === 'string' && isOptionalString(value.status_reason) && typeof value.stale === 'boolean' &&
    isObject(value.server) && typeof value.server.hostname === 'string' &&
    typeof value.server.os === 'string' && typeof value.server.arch === 'string' &&
    typeof value.version === 'string' && typeof value.agent_selector === 'string' &&
    isTimestamp(value.started_at) && isTimestamp(value.last_heartbeat) && isTimestamp(value.lease_expires_at);
}

export async function listControllers(url: string, credentials: Session, signal?: AbortSignal): Promise<Controller[]> {
  const body = await getJSON(url, '/api/v1/controllers', credentials, signal);
  if (!isObject(body) || !Array.isArray(body.items) || !body.items.every(isController)) {
    throw new ApiError('invalid_response');
  }
  return body.items;
}

export async function getController(url: string, credentials: Session, id: string, signal?: AbortSignal): Promise<Controller> {
  const body = await getJSON(url, `/api/v1/controllers/${encodeIdentifier(id)}`, credentials, signal);
  if (!isController(body) || body.id !== id) throw new ApiError('invalid_response');
  return body;
}

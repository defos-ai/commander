import type { Session } from '../connections/types';
import { ApiError, getJSON } from './client';
import type { Action, ActionRequirement } from './types';
import { encodeIdentifier, isIdentifier, isObject, isOptionalString } from './validation';

export function isActionRequirement(value: unknown): value is ActionRequirement {
  return isObject(value) && typeof value.kind === 'string' && typeof value.name === 'string' &&
    (value.kind !== 'secret' || /^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/.test(value.name)) &&
    (value.sensitive === undefined || typeof value.sensitive === 'boolean') &&
    (value.sensitive !== true || value.kind === 'configuration');
}

export function isAction(value: unknown): value is Action {
  return isObject(value) && isIdentifier(value.name) && typeof value.description === 'string' &&
    typeof value.status === 'string' && isOptionalString(value.status_reason) &&
    (value.required_secrets === undefined || (Array.isArray(value.required_secrets) &&
      value.required_secrets.every(name => typeof name === 'string'))) &&
    (value.missing_requirements === undefined || value.missing_requirements === null ||
      (Array.isArray(value.missing_requirements) && value.missing_requirements.every(isActionRequirement))) &&
    (value.input_schema === null || isObject(value.input_schema)) &&
    (value.output_schema === null || isObject(value.output_schema));
}

export async function listActions(url: string, credentials: Session, signal?: AbortSignal): Promise<Action[]> {
  const body = await getJSON(url, '/api/v1/actions', credentials, signal);
  if (!isObject(body) || !Array.isArray(body.items) || !body.items.every(isAction)) {
    throw new ApiError('invalid_response');
  }
  return body.items;
}

export async function getAction(url: string, credentials: Session, name: string, signal?: AbortSignal): Promise<Action> {
  const body = await getJSON(url, `/api/v1/actions/${encodeIdentifier(name)}`, credentials, signal);
  if (!isAction(body) || body.name !== name) throw new ApiError('invalid_response');
  return body;
}

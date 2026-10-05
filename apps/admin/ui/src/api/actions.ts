import type { Credentials } from '../connections/types';
import { ApiError, getJSON } from './client';
import type { Action, ActionRequirement } from './types';
import { encodeIdentifier, isIdentifier, isNullableStringArray, isObject, isOptionalString } from './validation';

function isRequirement(value: unknown): value is ActionRequirement {
  return isObject(value) && typeof value.kind === 'string' && typeof value.name === 'string' &&
    (value.secret === undefined || typeof value.secret === 'boolean');
}

export function isAction(value: unknown): value is Action {
  return isObject(value) && isIdentifier(value.name) && typeof value.description === 'string' &&
    typeof value.status === 'string' && isOptionalString(value.status_reason) &&
    isNullableStringArray(value.required_permissions) &&
    (value.missing_requirements === undefined || value.missing_requirements === null ||
      (Array.isArray(value.missing_requirements) && value.missing_requirements.every(isRequirement))) &&
    (value.input_schema === null || isObject(value.input_schema)) &&
    (value.output_schema === null || isObject(value.output_schema));
}

export async function listActions(url: string, credentials: Credentials, signal?: AbortSignal): Promise<Action[]> {
  const body = await getJSON(url, '/api/v1/actions', credentials, signal);
  if (!isObject(body) || !Array.isArray(body.items) || !body.items.every(isAction)) {
    throw new ApiError('invalid_response');
  }
  return body.items;
}

export async function getAction(url: string, credentials: Credentials, name: string, signal?: AbortSignal): Promise<Action> {
  const body = await getJSON(url, `/api/v1/actions/${encodeIdentifier(name)}`, credentials, signal);
  if (!isAction(body) || body.name !== name) throw new ApiError('invalid_response');
  return body;
}

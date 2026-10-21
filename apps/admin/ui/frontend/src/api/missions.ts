import type { Session } from '../connections/types';
import { ApiError, getJSON } from './client';
import type { Mission, MissionRule, MissionStep } from './types';

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every(item => typeof item === 'string');
}

function isNullableStringArray(value: unknown): value is string[] | null {
  return value === null || isStringArray(value);
}

function isStep(value: unknown): value is MissionStep {
  return isObject(value) && typeof value.id === 'string' && typeof value.objective === 'string' &&
    isNullableStringArray(value.agent_types) && isNullableStringArray(value.required_actions);
}

function isRule(name: string, value: unknown): value is MissionRule {
  if (!isObject(value) || Object.keys(value).some(key => key !== 'for_actions' && key !== 'params') ||
    !isStringArray(value.for_actions) || !value.for_actions.length ||
    value.for_actions.some(action => !action || action.trim() !== action) || new Set(value.for_actions).size !== value.for_actions.length) return false;
  const params = value.params;
  if (params !== undefined && (!isObject(params) || Object.keys(params).some(key => key !== 'decision_by'))) return false;
  if (name === 'aggregate-results' || name === 'same-agent') return params === undefined || params.decision_by === undefined;
  return name === 'decide-next-actions' && isObject(params) && params.decision_by === 'model';
}

function isMissionName(value: unknown): value is string {
  return typeof value === 'string' && value.trim() !== '' && value !== '.' && value !== '..';
}

export function isMission(value: unknown): value is Mission {
  return isObject(value) && isMissionName(value.name) && typeof value.objective === 'string' &&
    isNullableStringArray(value.required_actions) && isNullableStringArray(value.starting_actions) &&
    (value.optional_actions === undefined || isStringArray(value.optional_actions)) &&
    (value.input_schema === null || isObject(value.input_schema)) &&
    (value.output_schema === null || isObject(value.output_schema)) &&
    (value.steps === undefined || (Array.isArray(value.steps) && value.steps.every(isStep))) &&
    (value.rules === undefined || (isObject(value.rules) && Object.entries(value.rules).every(([name, rule]) => isRule(name, rule))));
}

export async function listMissions(url: string, credentials: Session, signal?: AbortSignal): Promise<Mission[]> {
  const body = await getJSON(url, '/api/v1/missions', credentials, signal);
  if (!isObject(body) || !Array.isArray(body.items) || !body.items.every(isMission)) {
    throw new ApiError('invalid_response');
  }
  return body.items;
}

export async function getMission(url: string, credentials: Session, name: string, signal?: AbortSignal): Promise<Mission> {
  if (!isMissionName(name)) throw new ApiError('invalid_response');
  const body = await getJSON(url, `/api/v1/missions/${encodeURIComponent(name)}`, credentials, signal);
  if (!isMission(body) || body.name !== name) throw new ApiError('invalid_response');
  return body;
}

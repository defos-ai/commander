import type { Session } from '../connections/types';
import { ApiError, getJSON } from './client';
import type { Mission, MissionAutomation, MissionStep } from './types';

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

function isAutomation(value: unknown): value is MissionAutomation {
  return isObject(value) && (value.run === undefined || isStringArray(value.run)) &&
    (value.continue === undefined || typeof value.continue === 'string') &&
    (value.decide === undefined || typeof value.decide === 'string') &&
    (value.on_empty === undefined || typeof value.on_empty === 'string');
}

function isMissionName(value: unknown): value is string {
  return typeof value === 'string' && value.trim() !== '' && value !== '.' && value !== '..';
}

export function isMission(value: unknown): value is Mission {
  return isObject(value) && isMissionName(value.name) && typeof value.objective === 'string' &&
    isNullableStringArray(value.required_actions) && isNullableStringArray(value.starting_actions) &&
    (value.input_schema === null || isObject(value.input_schema)) &&
    (value.output_schema === null || isObject(value.output_schema)) &&
    (value.finalize === undefined || typeof value.finalize === 'string') &&
    (value.start === undefined || (isObject(value.start) && typeof value.start.action_name === 'string' &&
      (value.start.input === undefined || isObject(value.start.input)) &&
      (value.start.reason === undefined || typeof value.start.reason === 'string'))) &&
    (value.steps === undefined || (Array.isArray(value.steps) && value.steps.every(isStep))) &&
    (value.automations === undefined || (isObject(value.automations) && Object.values(value.automations).every(isAutomation)));
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

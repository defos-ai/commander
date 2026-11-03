import type { MissionPolicies, SharingRoute } from '../api/types';

function object(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function actionNames(value: unknown): value is string[] {
  return Array.isArray(value) && value.every(name => typeof name === 'string' && name !== '' && name.trim() === name) &&
    new Set(value).size === value.length;
}

function actionPolicy(value: unknown, fields: string[]): boolean {
  return value == null || (object(value) && Object.keys(value).every(key => fields.includes(key)) &&
    fields.every(field => value[field] == null || actionNames(value[field])));
}

function sharingRoute(value: unknown): value is SharingRoute {
  return object(value) && Object.keys(value).every(key => ['from_actions', 'to_actions', 'include'].includes(key)) &&
    actionNames(value.from_actions) && value.from_actions.length > 0 &&
    actionNames(value.to_actions) && value.to_actions.length > 0 &&
    Array.isArray(value.include) && value.include.length > 0 &&
    value.include.every(kind => kind === 'results' || kind === 'artifacts' || kind === 'decisions') &&
    new Set(value.include).size === value.include.length;
}

function completionPolicy(value: unknown): boolean {
  return value == null || (object(value) && Object.keys(value).every(key => key === 'decision_by') &&
    (value.decision_by === 'model' || value.decision_by === 'operator'));
}

export function isMissionPolicies(value: unknown): value is MissionPolicies | null | undefined {
  return value == null || (object(value) && Object.keys(value).every(key => ['continuation', 'completion', 'sharing'].includes(key)) &&
    actionPolicy(value.continuation, ['aggregate_results', 'model_review']) &&
    completionPolicy(value.completion) &&
    (value.sharing == null || (Array.isArray(value.sharing) && value.sharing.every(sharingRoute))));
}

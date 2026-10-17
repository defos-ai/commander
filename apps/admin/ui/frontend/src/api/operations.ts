import type { Session } from '../connections/types';
import { ApiError, getJSON } from './client';
import { isMission } from './missions';
import type {
  Operation, OperationActionOutput, OperationActionReadiness, OperationAgentReadiness,
  OperationArtifact, OperationArtifactFile, OperationContinuation, OperationEvent,
  OperationNextAction, OperationPreflight, OperationQuestion, OperationTask,
} from './operation-types';
import type { ActionRequirement } from './types';
import { encodeIdentifier, isIdentifier, isNullableStringArray, isObject, isOptionalString } from './validation';

function isNullableArray<T>(value: unknown, isItem: (item: unknown) => item is T): value is T[] | null {
  return value === null || (Array.isArray(value) && value.every(isItem));
}

function isDate(value: unknown): value is string {
  return typeof value === 'string' && Number.isFinite(Date.parse(value));
}

function isOptionalDate(value: unknown): value is string | null | undefined {
  return value === undefined || value === null || isDate(value);
}

function isUnsignedInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

function isNextAction(value: unknown): value is OperationNextAction {
  return isObject(value) && typeof value.action_name === 'string' && isOptionalString(value.reason);
}

function isArtifactFile(value: unknown): value is OperationArtifactFile {
  return isObject(value) && isIdentifier(value.id) && typeof value.name === 'string' &&
    typeof value.media_type === 'string' && isUnsignedInteger(value.size) && typeof value.sha256 === 'string';
}

export function isArtifact(value: unknown): value is OperationArtifact {
  return isObject(value) && isIdentifier(value.id) && typeof value.name === 'string' &&
    typeof value.agent_id === 'string' && typeof value.action_name === 'string' &&
    isOptionalString(value.command_id) && isDate(value.created_at) && isNullableArray(value.files, isArtifactFile);
}

function isActionOutput(value: unknown): value is OperationActionOutput {
  return isObject(value) && typeof value.action_name === 'string' && 'output' in value &&
    (value.artifacts === undefined || isNullableArray(value.artifacts, isArtifact)) &&
    (value.next_actions === undefined || isNullableArray(value.next_actions, isNextAction));
}

function isContinuation(value: unknown): value is OperationContinuation {
  return isObject(value) && isUnsignedInteger(value.version) && typeof value.decision_by === 'string' &&
    typeof value.status === 'string' && isNullableArray(value.proposed_actions, isNextAction) &&
    isNullableStringArray(value.created_task_ids) && isOptionalString(value.question_id);
}

function isTask(value: unknown): value is OperationTask {
  return isObject(value) && isIdentifier(value.id) && typeof value.operation_id === 'string' &&
    isOptionalString(value.parent_task_id) && isOptionalString(value.step_id) &&
    typeof value.agent_id === 'string' && typeof value.action_name === 'string' &&
    isOptionalString(value.command_id) && isOptionalDate(value.deadline_at) &&
    typeof value.status === 'string' && isOptionalString(value.error) &&
    isDate(value.started_at) && isOptionalDate(value.finished_at) &&
    (value.result === undefined || value.result === null || isActionOutput(value.result)) &&
    (value.continuation === undefined || value.continuation === null || isContinuation(value.continuation));
}

function isQuestion(value: unknown): value is OperationQuestion {
  return isObject(value) && isIdentifier(value.id) && typeof value.status === 'string' &&
    isOptionalString(value.step_id) && isOptionalString(value.task_id) && typeof value.text === 'string' &&
    (value.options === undefined || isNullableStringArray(value.options)) &&
    isOptionalString(value.answer) && isDate(value.asked_at) &&
    isOptionalDate(value.answered_at) && isDate(value.expires_at);
}

function isEvent(value: unknown): value is OperationEvent {
  return isObject(value) && isUnsignedInteger(value.sequence) && typeof value.operation_id === 'string' &&
    typeof value.type === 'string' && typeof value.status === 'string' &&
    isOptionalString(value.task_id) && isOptionalString(value.question_id) &&
    isOptionalString(value.message) && isDate(value.created_at);
}

function isRequirement(value: unknown): value is ActionRequirement {
  return isObject(value) && typeof value.kind === 'string' && typeof value.name === 'string' &&
    (value.secret === undefined || typeof value.secret === 'boolean');
}

function isAgentReadiness(value: unknown): value is OperationAgentReadiness {
  return isObject(value) && typeof value.agent_id === 'string' && typeof value.reason === 'string' &&
    (value.agent_type === undefined || (typeof value.agent_type === 'string' && value.agent_type.length <= 63 && /^[a-z0-9]+(-[a-z0-9]+)*$/.test(value.agent_type))) &&
    (value.missing_requirements === undefined || isNullableArray(value.missing_requirements, isRequirement)) &&
    (value.missing_secrets === undefined || value.missing_secrets === null || (Array.isArray(value.missing_secrets) &&
      value.missing_secrets.every(name => typeof name === 'string' && /^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/.test(name)) &&
      (value.missing_secrets.length === 0 || typeof value.agent_type === 'string')));
}

function isActionReadiness(value: unknown): value is OperationActionReadiness {
  return isObject(value) && typeof value.action_name === 'string' &&
    (value.ready_agent_ids === undefined || isNullableStringArray(value.ready_agent_ids)) &&
    (value.unavailable_agents === undefined || isNullableArray(value.unavailable_agents, isAgentReadiness));
}

function isPreflight(value: unknown): value is OperationPreflight {
  return isObject(value) && typeof value.ready === 'boolean' && isUnsignedInteger(value.version) &&
    isNullableArray(value.actions, isActionReadiness);
}

export function isOperation(value: unknown): value is Operation {
  return isObject(value) && isIdentifier(value.id) && isMission(value.mission) && 'input' in value &&
    typeof value.status === 'string' && isOptionalString(value.report_id) && isOptionalString(value.error) &&
    isDate(value.created_at) && isDate(value.updated_at) &&
    (value.preflight === undefined || value.preflight === null || isPreflight(value.preflight)) &&
    isNullableArray(value.tasks, isTask) && isNullableArray(value.questions, isQuestion) &&
    isNullableArray(value.events, isEvent);
}

export async function listOperations(url: string, credentials: Session, signal?: AbortSignal): Promise<Operation[]> {
  const body = await getJSON(url, '/api/v1/operations', credentials, signal);
  if (!isObject(body) || !Array.isArray(body.items) || !body.items.every(isOperation)) {
    throw new ApiError('invalid_response');
  }
  return body.items;
}

export async function getOperation(url: string, credentials: Session, id: string, signal?: AbortSignal): Promise<Operation> {
  const body = await getJSON(url, `/api/v1/operations/${encodeIdentifier(id)}`, credentials, signal);
  if (!isOperation(body) || body.id !== id) throw new ApiError('invalid_response');
  return body;
}

import type { Session } from '../connections/types';
import { ApiError, getJSON, getJSONDocument } from './client';
import { isActionRequirement } from './actions';
import { isMission } from './missions';
import type {
  Operation, OperationActionOutput, OperationActionReadiness, OperationAffinity, OperationAgentReadiness,
  OperationArtifact, OperationArtifactFile, OperationContinuation, OperationEvent,
  OperationNextAction, OperationPreflight, OperationProgress, OperationQuestion, OperationTask,
  OperationSessionIntent, OperationSessionReference, OperationTaskPlacement,
} from './operation-types';
import { encodeIdentifier, isIdentifier, isNullableStringArray, isObject, isOptionalString } from './validation';
import { readJSONObject } from '../missions/draft';

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
    (value.session === undefined || value.session === null || isSessionReference(value.session)) &&
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
    isOptionalString(value.parent_task_id) &&
    typeof value.agent_id === 'string' && typeof value.action_name === 'string' && hasScopedRequirements(value) &&
    isOptionalString(value.command_id) && isOptionalDate(value.deadline_at) &&
    typeof value.status === 'string' && isOptionalString(value.error) &&
    isDate(value.started_at) && isOptionalDate(value.finished_at) &&
    (value.session_intent === undefined || value.session_intent === null || isSessionIntent(value.session_intent)) &&
    (value.result === undefined || value.result === null || isActionOutput(value.result)) &&
    (value.continuation === undefined || value.continuation === null || isContinuation(value.continuation));
}

function isQuestion(value: unknown): value is OperationQuestion {
  return isObject(value) && isIdentifier(value.id) && typeof value.status === 'string' &&
    isOptionalString(value.task_id) && typeof value.text === 'string' &&
    (value.options === undefined || isNullableStringArray(value.options)) &&
    isOptionalString(value.answer) &&
    (value.answered_by === undefined || value.answered_by === 'operator' || value.answered_by === 'model') && isDate(value.asked_at) &&
    isOptionalDate(value.answered_at) && isDate(value.fallback_at);
}

function isEvent(value: unknown): value is OperationEvent {
  return isObject(value) && isUnsignedInteger(value.sequence) && typeof value.operation_id === 'string' &&
    typeof value.type === 'string' && typeof value.status === 'string' &&
    isOptionalString(value.task_id) && isOptionalString(value.question_id) &&
    isOptionalString(value.message) && isDate(value.created_at);
}

function isAgentReadiness(value: unknown): value is OperationAgentReadiness {
  return isObject(value) && typeof value.agent_id === 'string' && typeof value.reason === 'string' &&
    hasScopedRequirements(value);
}

function hasScopedRequirements(value: Record<string, unknown>): boolean {
  return (
    (value.agent_type === undefined || (typeof value.agent_type === 'string' && value.agent_type.length <= 63 && /^[a-z0-9]+(-[a-z0-9]+)*$/.test(value.agent_type))) &&
    (value.missing_requirements === undefined || isNullableArray(value.missing_requirements, isActionRequirement)) &&
    (!value.missing_requirements?.some(requirement => requirement.kind === 'secret') || typeof value.agent_type === 'string')
  );
}

function isActionReadiness(value: unknown): value is OperationActionReadiness {
  return isObject(value) && typeof value.action_name === 'string' &&
    (value.ready_agent_ids === undefined || isNullableStringArray(value.ready_agent_ids)) &&
    (value.unavailable_agents === undefined || isNullableArray(value.unavailable_agents, isAgentReadiness));
}

function isPreflight(value: unknown): value is OperationPreflight {
  return isObject(value) && typeof value.ready === 'boolean' && isUnsignedInteger(value.version) &&
    isNullableArray(value.actions, isActionReadiness) &&
    (value.tasks === undefined || isNullableArray(value.tasks, isTaskPlacement));
}

function isUUID(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);
}

function isAgentID(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 63 && /^[a-z0-9]+(?:-[a-z0-9]+)*-agent-[0-9a-f]{8}$/.test(value);
}

function isSessionIntent(value: unknown): value is OperationSessionIntent {
  if (!isObject(value) || typeof value.mode !== 'string' || typeof value.scope_id !== 'string' ||
    !isOptionalString(value.id) || !isOptionalString(value.error)) return false;
  // Admission retains failed automatic proposals as an error with empty identity fields.
  if (value.error) return true;
  return ['create', 'reuse', 'use'].includes(value.mode) && isUUID(value.scope_id) &&
    (value.id === undefined || isUUID(value.id));
}

function isSessionReference(value: unknown): value is OperationSessionReference {
  return isObject(value) && isUUID(value.id) && isUUID(value.scope_id) && isAgentID(value.owner_agent_id);
}

function isTaskPlacement(value: unknown): value is OperationTaskPlacement {
  return isObject(value) && isIdentifier(value.task_id) &&
    (value.owner_agent_id === undefined || isAgentID(value.owner_agent_id)) &&
    isOptionalString(value.reason) && hasScopedRequirements(value);
}

function isAffinity(value: unknown): value is OperationAffinity {
  return isObject(value) && Array.isArray(value.for_actions) && value.for_actions.length > 0 &&
    value.for_actions.every(action => typeof action === 'string' && action !== '' && action.trim() === action) &&
    new Set(value.for_actions).size === value.for_actions.length &&
    (value.agent_id === undefined || isIdentifier(value.agent_id)) &&
    (value.error === undefined || (typeof value.error === 'string' && value.error.trim() !== '')) &&
    (value.error === undefined || value.agent_id === undefined);
}

function isProgress(value: unknown): value is OperationProgress {
  return isObject(value) && isObject(value.task_counts) && Object.values(value.task_counts).every(isUnsignedInteger) &&
    isUnsignedInteger(value.total_tasks) && isUnsignedInteger(value.finished_tasks) &&
    isUnsignedInteger(value.pending_decisions) && isUnsignedInteger(value.pending_questions) && typeof value.settled === 'boolean';
}

export function isOperation(value: unknown): value is Operation {
  return isObject(value) && isIdentifier(value.id) && isMission(value.mission) && 'input' in value &&
    typeof value.status === 'string' && isOptionalString(value.report_id) && isOptionalString(value.error) &&
    (value.completed_by === undefined || value.completed_by === 'operator' || value.completed_by === 'model') &&
    isDate(value.created_at) && isDate(value.updated_at) && isProgress(value.progress) &&
    (value.affinity === undefined || isAffinity(value.affinity)) &&
    (value.preflight === undefined || value.preflight === null || isPreflight(value.preflight)) &&
    isNullableArray(value.tasks, isTask) && isNullableArray(value.questions, isQuestion) &&
    isNullableArray(value.events, isEvent);
}

export async function listOperations(url: string, credentials: Session, signal?: AbortSignal): Promise<Operation[]> {
  const body = await getJSON(url, '/api/v1/operations', credentials, signal);
  if (!isObject(body) || !Array.isArray(body.items) || !body.items.every(isOperation)) {
    throw new ApiError('invalid_response');
  }
  for (const operation of body.items) {
    delete operation.completionDraftText;
  }
  return body.items;
}

export async function getOperation(url: string, credentials: Session, id: string, signal?: AbortSignal): Promise<Operation> {
  const document = await getJSONDocument(url, `/api/v1/operations/${encodeIdentifier(id)}`, credentials, signal);
  const body = document.value;
  if (!isOperation(body) || body.id !== id) throw new ApiError('invalid_response');
  delete body.completionDraftText;
  if ('completion_draft' in body) {
    try {
      const span = readJSONObject(document.text).fields.get('completion_draft')!;
      body.completionDraftText = document.text.slice(span.start, span.end);
    } catch { throw new ApiError('invalid_response'); }
  }
  return body;
}

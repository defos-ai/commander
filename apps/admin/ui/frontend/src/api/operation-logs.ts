import type { Session } from '../connections/types';
import { ApiError, getJSON } from './client';
import { isObject } from './validation';

export interface OperationLog {
  id: string;
  agent_id: string;
  command_id: string;
  action_name: string;
  operation_id: string;
  task_id: string;
  message: string;
  created_at: string;
}

export interface OperationLogFilters {
  task_id?: string;
  agent_id?: string;
  action_name?: string;
}

export interface OperationLogQuery extends OperationLogFilters {
  order?: 'asc' | 'desc';
  cursor?: string;
  limit?: number;
}

export interface OperationLogPage {
  items: OperationLog[];
  next_cursor: string;
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const agentIDPattern = /^[a-z0-9]+(-[a-z0-9]+)*-agent-[0-9a-f]{8}$/;
const unicodePattern = /^(?:[\u0000-\ud7ff\ue000-\uffff]|[\ud800-\udbff][\udc00-\udfff])*$/;
const nonWhitespacePattern = /[^\u0009-\u000d \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]/;
const queryKeys = ['task_id', 'agent_id', 'action_name', 'order', 'cursor', 'limit'];

function isUUID(value: unknown): value is string {
  return typeof value === 'string' && uuidPattern.test(value);
}

function isAgentID(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 63 && agentIDPattern.test(value);
}

function isText(value: unknown): value is string {
  return typeof value === 'string' && unicodePattern.test(value) && nonWhitespacePattern.test(value);
}

function isTimestamp(value: unknown): value is string {
  if (typeof value !== 'string') return false;
  const parts = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(?:Z|[+-](\d{2}):(\d{2}))$/.exec(value);
  if (!parts) return false;
  const [year, month, day, hour, minute, second] = parts.slice(1, 7).map(Number);
  const offsetHour = Number(parts[8] ?? 0), offsetMinute = Number(parts[9] ?? 0);
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  const instant = Date.parse(value);
  return year >= 0 && month >= 1 && month <= 12 && day >= 1 && day <= days[month - 1] &&
    hour <= 23 && minute <= 59 && second <= 59 && offsetHour <= 23 && offsetMinute <= 59 &&
    Number.isFinite(instant) && (instant !== Date.parse('0001-01-01T00:00:00Z') || /[1-9]/.test(parts[7] ?? ''));
}

function validateQuery(operationID: string, query: OperationLogQuery) {
  if (!isUUID(operationID) || !isObject(query) || Object.keys(query).some(key => !queryKeys.includes(key)) ||
    query.task_id !== undefined && !isUUID(query.task_id) ||
    query.agent_id !== undefined && !isAgentID(query.agent_id) ||
    query.action_name !== undefined && !isText(query.action_name) ||
    query.order !== undefined && query.order !== 'asc' && query.order !== 'desc' ||
    query.cursor !== undefined && query.cursor !== '' && !isUUID(query.cursor) ||
    query.limit !== undefined && (typeof query.limit !== 'number' || !Number.isInteger(query.limit) || query.limit < 1 || query.limit > 1000)) {
    throw new ApiError('invalid_response');
  }
}

function readLog(value: unknown, operationID: string, query: OperationLogQuery): OperationLog {
  if (!isObject(value) || !isUUID(value.id) || !isAgentID(value.agent_id) || !isUUID(value.command_id) ||
    !isText(value.action_name) || !isUUID(value.operation_id) || value.operation_id !== operationID ||
    !isUUID(value.task_id) ||
    !isText(value.message) || new TextEncoder().encode(value.message).length > 2048 || !isTimestamp(value.created_at) ||
    query.task_id !== undefined && value.task_id !== query.task_id ||
    query.agent_id !== undefined && value.agent_id !== query.agent_id ||
    query.action_name !== undefined && value.action_name !== query.action_name || value.id === query.cursor) {
    throw new ApiError('invalid_response');
  }
  const item: OperationLog = {
    id: value.id, agent_id: value.agent_id, command_id: value.command_id,
    action_name: value.action_name, operation_id: value.operation_id, task_id: value.task_id,
    message: value.message, created_at: value.created_at,
  };
  return item;
}

export async function listOperationLogs(
  url: string, credentials: Session, operationID: string, query: OperationLogQuery = {}, signal?: AbortSignal,
): Promise<OperationLogPage> {
  validateQuery(operationID, query);
  const criteria = { ...query };
  const params = new URLSearchParams();
  for (const name of ['task_id', 'agent_id', 'action_name', 'order', 'cursor', 'limit'] as const) {
    if (criteria[name] !== undefined) params.set(name, String(criteria[name]));
  }
  const path = `/api/v1/operations/${operationID}/logs${params.size ? `?${params}` : ''}`;
  const body = await getJSON(url, path, credentials, signal);
  if (!isObject(body) || !Array.isArray(body.items) || body.items.length > (criteria.limit ?? 100) ||
    typeof body.next_cursor !== 'string') throw new ApiError('invalid_response');
  const items = body.items.map(item => readLog(item, operationID, criteria));
  const nextCursor = items.length ? items[items.length - 1].id : criteria.cursor ?? '';
  if (new Set(items.map(item => item.id)).size !== items.length || body.next_cursor !== nextCursor) {
    throw new ApiError('invalid_response');
  }
  return { items, next_cursor: body.next_cursor };
}

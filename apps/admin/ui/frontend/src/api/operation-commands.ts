import { expireSession, proxyURL } from '../connections/client';
import type { Session } from '../connections/types';
import type { Operation } from './operation-types';
import { isOperation } from './operations';
import { encodeIdentifier, isIdentifier, isObject } from './validation';

export type CommandErrorKind = 'authentication' | 'forbidden' | 'not_found' | 'invalid_input' | 'conflict' | 'unknown';
const errorCodes = [
  'operation_conflict', 'operation_finished', 'question_answered', 'question_operator_waiting',
  'invalid_request', 'invalid_input', 'invalid_operation_id', 'invalid_question_id', 'invalid_request_origin',
  'mission_not_found', 'operation_not_found', 'question_not_found',
] as const;
export type CommandErrorCode = typeof errorCodes[number];

export class CommandError extends Error {
  constructor(readonly kind: CommandErrorKind, readonly code?: CommandErrorCode) {
    super(kind);
    this.name = 'CommandError';
  }
}

export interface StartOperationInput {
  operation_id: string;
  mission_name: string;
  input: unknown;
}

function commandIdentifier(value: string): string {
  try { return encodeIdentifier(value); }
  catch { throw new CommandError('invalid_input'); }
}

function responseError(status: number, body: unknown): CommandError {
  const kind: CommandErrorKind = status === 401 ? 'authentication'
    : status === 403 ? 'forbidden'
      : status === 404 ? 'not_found'
        : status === 409 ? 'conflict'
          : status === 400 || status === 413 || status === 415 || status === 422 ? 'invalid_input' : 'unknown';
  const code = isObject(body) && typeof body.error === 'string' && (errorCodes as readonly string[]).includes(body.error)
    ? body.error as CommandErrorCode : undefined;
  return new CommandError(kind, kind === 'unknown' ? undefined : code);
}

async function postOperation(url: string, path: string, credentials: Session, operationID: string, body: unknown, signal?: AbortSignal): Promise<Operation> {
  if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
  let endpoint: string;
  let encodedBody: string;
  try {
    endpoint = proxyURL(url, path, credentials);
    encodedBody = JSON.stringify(body);
  } catch {
    throw new CommandError('invalid_input');
  }

  const controller = new AbortController();
  const abort = () => controller.abort();
  let rejectAborted: () => void;
  const aborted = new Promise<never>((_, reject) => {
    rejectAborted = () => reject(new CommandError('unknown'));
    controller.signal.addEventListener('abort', rejectAborted, { once: true });
  });
  signal?.addEventListener('abort', abort, { once: true });
  const timeout = setTimeout(abort, 30_000);

  async function send() {
    const response = await fetch(endpoint, {
      method: 'POST', headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-Commander-UI': '1' },
      credentials: 'same-origin', redirect: 'error', cache: 'no-store', body: encodedBody, signal: controller.signal,
    });
    if (response.status === 401 && !controller.signal.aborted) expireSession(url);
    let result: unknown;
    try { result = await response.json(); }
    catch { if (response.ok) throw new CommandError('unknown'); }
    if (!response.ok) throw responseError(response.status, result);
    if (!isOperation(result) || result.id !== operationID) throw new CommandError('unknown');
    return result;
  }

  try {
    return await Promise.race([send(), aborted]);
  } catch (error) {
    if (error instanceof CommandError) throw error;
    throw new CommandError('unknown');
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener('abort', abort);
    controller.signal.removeEventListener('abort', rejectAborted!);
  }
}

export async function startOperation(url: string, credentials: Session, input: StartOperationInput, signal?: AbortSignal): Promise<Operation> {
  if (!isIdentifier(input.operation_id) || !input.mission_name.trim() || input.input === undefined) throw new CommandError('invalid_input');
  return postOperation(url, '/api/v1/operations', credentials, input.operation_id, input, signal);
}

export async function cancelOperation(url: string, credentials: Session, id: string, signal?: AbortSignal): Promise<Operation> {
  return postOperation(url, `/api/v1/operations/${commandIdentifier(id)}/cancel`, credentials, id, {}, signal);
}

export async function answerQuestion(url: string, credentials: Session, operationID: string, questionID: string, answer: string, signal?: AbortSignal): Promise<Operation> {
  if (!answer.trim()) throw new CommandError('invalid_input');
  return postOperation(url, `/api/v1/operations/${commandIdentifier(operationID)}/questions/${commandIdentifier(questionID)}/answer`, credentials, operationID, { answer }, signal);
}

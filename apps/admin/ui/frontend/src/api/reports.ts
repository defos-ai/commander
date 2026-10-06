import type { Session } from '../connections/types';
import { ApiError, getJSON } from './client';
import { encodeIdentifier, isIdentifier, isObject } from './validation';

export interface Report {
  id: string;
  operation_id: string;
  mission: string;
  output: unknown;
  created_at: string;
}

function isReport(value: unknown): value is Report {
  return isObject(value) && isIdentifier(value.id) && isIdentifier(value.operation_id) && isIdentifier(value.mission) &&
    'output' in value && typeof value.created_at === 'string' && Number.isFinite(Date.parse(value.created_at));
}

export async function getOperationReport(url: string, credentials: Session, operationID: string, signal?: AbortSignal): Promise<Report> {
  const body = await getJSON(url, `/api/v1/operations/${encodeIdentifier(operationID)}/report`, credentials, signal);
  if (!isReport(body) || body.operation_id !== operationID) {
    throw new ApiError('invalid_response');
  }
  return body;
}

import { ApiError } from './client';

export function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function isNullableStringArray(value: unknown): value is string[] | null {
  return value === null || (Array.isArray(value) && value.every(item => typeof item === 'string'));
}

export function isOptionalString(value: unknown): value is string | undefined {
  return value === undefined || typeof value === 'string';
}

export function isIdentifier(value: unknown): value is string {
  return typeof value === 'string' && value.trim() !== '' && value !== '.' && value !== '..';
}

export function encodeIdentifier(value: string): string {
  if (!isIdentifier(value)) throw new ApiError('invalid_response');
  try {
    return encodeURIComponent(value);
  } catch {
    throw new ApiError('invalid_response');
  }
}

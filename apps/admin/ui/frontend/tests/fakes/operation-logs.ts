import { vi } from 'vitest';
import { listOperationLogs, type OperationLog, type OperationLogPage, type OperationLogQuery } from '../../src/api/operation-logs';
import type { Session } from '../../src/connections/types';

export class LogRequest {
  readonly url: string;
  readonly operationID: string;
  readonly signal?: AbortSignal;
  readonly promise: Promise<OperationLogPage>;
  #credentials: Session;
  #query: OperationLogQuery;
  #resolve!: (page: OperationLogPage) => void;
  #reject!: (error: unknown) => void;
  #settled = false;

  constructor(url: string, credentials: Session, operationID: string, query: OperationLogQuery, signal?: AbortSignal) {
    this.url = url;
    this.#credentials = { ...credentials };
    this.operationID = operationID;
    this.#query = { ...query };
    this.signal = signal;
    this.promise = new Promise((resolve, reject) => { this.#resolve = resolve; this.#reject = reject; });
  }

  get credentials() { return { ...this.#credentials }; }
  get query() { return { ...this.#query }; }

  // Aborted requests remain controllable to exercise late-response guards.
  reply(items: OperationLog[], nextCursor = items.at(-1)?.id ?? this.#query.cursor ?? '') {
    if (this.#settled) throw new Error('Log request was already settled');
    this.#settled = true;
    this.#resolve({ items: structuredClone(items), next_cursor: nextCursor });
  }

  fail(error: unknown) {
    if (this.#settled) throw new Error('Log request was already settled');
    this.#settled = true;
    this.#reject(error);
  }
}

export function operationLogAPI() {
  const requests: LogRequest[] = [];
  const read = vi.mocked(listOperationLogs);
  read.mockReset();
  read.mockImplementation((url, credentials, operationID, query = {}, signal) => {
    const request = new LogRequest(url, credentials, operationID, query, signal);
    requests.push(request);
    return request.promise;
  });
  return {
    count: () => requests.length,
    request(index: number) {
      const request = requests[index];
      if (!request) throw new Error(`Log request ${index} was not issued (${requests.length} total)`);
      return request;
    },
  };
}

export function logScrollPosition(element: HTMLElement, height = 1000, viewport = 200) {
  let top = Math.max(0, height - viewport);
  Object.defineProperties(element, {
    scrollHeight: { configurable: true, get: () => height },
    clientHeight: { configurable: true, get: () => viewport },
    scrollTop: { configurable: true, get: () => top, set: (value: number) => { top = Math.max(0, Math.min(value, height - viewport)); } },
  });
  element.scrollTo = (_options?: ScrollToOptions | number, y?: number) => {
    const value = typeof _options === 'object' ? _options.top ?? top : y ?? top;
    element.scrollTop = value;
  };
  return { top: () => top, bottom: height - viewport };
}

export function documentVisibility() {
  let hidden = false;
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => hidden ? 'hidden' : 'visible');
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  return {
    set(value: 'hidden' | 'visible') {
      hidden = value === 'hidden';
      document.dispatchEvent(new Event('visibilitychange'));
    },
  };
}

import { vi } from 'vitest';
import type { Operation, OperationArtifact } from '../../src/api/operation-types';
import type { Report } from '../../src/api/reports';
import { upstreamURL } from './proxy';

interface Defaults {
  operation: Operation;
  report: Report;
  artifact: OperationArtifact;
  now: string;
}

export function fixture(defaults: Defaults, respond?: (url: string, options: RequestInit) => Response | Promise<Response> | undefined) {
  const { operation, report, artifact, now } = structuredClone(defaults);

  const fetch = vi.fn(async (input: RequestInfo | URL, options: RequestInit = {}) => {
    const url = upstreamURL(input);
    const override = respond?.(url, options);
    if (override) return override;
    if (url.endsWith('/operations')) return Response.json({ items: [operation] });
    if (url.endsWith('/report')) return Response.json(report);
    if (url.endsWith('/artifacts/artifact-page')) return Response.json(artifact);
    if (url.endsWith('/graph')) return Response.json({ operation_id: operation.id, status: operation.status, updated_at: now,
      nodes: [{ id: `artifact:${artifact.id}`, kind: 'artifact', artifact }], edges: [] });
    return Response.json(operation);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

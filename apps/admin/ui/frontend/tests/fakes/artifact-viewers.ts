import { vi } from 'vitest';
import type { OperationArtifact } from '../../src/api/operation-types';
import { upstreamURL } from './proxy';

export function artifactAPI(defaults: { artifact: OperationArtifact }, fileReply?: (fileID: string, options: RequestInit) => Response | Promise<Response>) {
  const { artifact } = structuredClone(defaults);

  const fetch = vi.fn(async (input: RequestInfo | URL, options: RequestInit = {}) => {
    const address = upstreamURL(input);
    if (address.includes('/files/')) return fileReply!(address.split('/').at(-1)!, options);
    return Response.json(artifact);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

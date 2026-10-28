import type { OperationArtifact } from '../../src/api/operation-types';
import { artifactAPI as fakeAPI } from '../fakes/artifact-viewers';

export const url = 'https://commander.example.test';

export const credentials = { username: 'operator', url };

export const artifact: OperationArtifact = { id: 'artifact-1', name: 'Page evidence', agent_id: 'agent-one', action_name: 'capture-page', created_at: '2026-10-06T10:00:00Z', files: [
  { id: 'text', name: 'page.html', media_type: 'text/html', size: 100, sha256: 'a'.repeat(64) },
  { id: 'image', name: 'page.png', media_type: 'image/png', size: 50, sha256: 'b'.repeat(64) },
] };

export function artifactAPI(fileReply?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ artifact }, fileReply);
}

import type { OperationArtifact } from '../../src/api/operation-types';
import type { Report } from '../../src/api/reports';

export const url = 'https://commander.example/team';

export const credentials = { url, username: 'operator' };

export const artifact: OperationArtifact = {
  id: 'artifact-1', name: 'Website screenshot', agent_id: 'agent-1', action_name: 'capture-page',
  created_at: '2026-10-05T12:00:00Z',
  files: [{ id: 'file-1', name: 'desktop.png', media_type: 'image/png', size: 4, sha256: 'abcd' }],
};

export const report: Report = {
  id: 'report-1', operation_id: 'operation-1', mission: 'review-website-with-browser',
  output: { summary: 'Review complete' }, created_at: '2026-10-05T12:00:00Z',
};

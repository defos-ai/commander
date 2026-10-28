import {
  type PendingSecretRequest, type SecretMetadata
} from '../../src/api/secrets';

export const url = 'https://commander.example/team';

export const credentials = { url, username: 'operator' };

export const key = { agent_type: 'builder-agent', name: 'GITHUB_TOKEN' };

export const id = '5cde98e0-23d3-447d-ad59-2238b244f689';

export const metadata: SecretMetadata = {
  ...key, id, version: 1, status: 'available', created_at: '2026-10-17T10:00:00Z',
  updated_at: '2026-10-17T10:00:00.123456789Z', expires_at: '2027-10-17T12:00:00+02:00',
};

export const pending: PendingSecretRequest = {
  ...key, id, agent_id: 'builder-agent-0123abcd', requested_at: '2026-10-17T10:00:00Z',
  expires_at: '2026-10-17T10:01:00Z', status: 'needs_value',
};

export const secretValue = 'test-private-value-not-for-display';

export const createInput = { ...key, value: secretValue };

export const replacement = { id, version: 1, value: secretValue };

export const itemPath = '/api/v1/secrets/builder-agent/GITHUB_TOKEN';

import { render } from '@testing-library/react';
import { vi } from 'vitest';
import type { PendingSecretRequest, SecretMetadata } from '../../src/api/secrets';
import { SecretsPage } from '../../src/pages/SecretsPage';
import { fixture as fakeAPI } from '../fakes/secrets';

export const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };

export const credentials = { username: 'operator', url: connection.url };

export const secretValue = 'private-test-token-never-in-metadata';

export const secret: SecretMetadata = {
  id: '12345678-1234-4234-8234-123456789abc', name: 'GITHUB_TOKEN', version: 7,
  created_at: '2026-10-01T10:00:00Z', updated_at: '2026-10-02T10:00:00Z', status: 'available',
  expires_at: undefined as string | undefined,
};

export const pending: PendingSecretRequest = {
  id: '22345678-1234-4234-8234-123456789abc', agent_type: 'builder-agent', name: secret.name,
  agent_id: 'builder-agent-12345678', requested_at: '2026-10-01T10:00:00Z', expires_at: '2099-01-01T00:00:00Z',
  status: 'needs_value',
};

export const secretsPath = '/api/v1/secrets';

export const requestsPath = '/api/v1/secret-requests';

export const secretPath = `${secretsPath}/${secret.name}`;

export function view() {
  return render(<SecretsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
}

export function fixture(respond?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ pending, secret, requestsPath, secretsPath, secretPath }, respond);
}

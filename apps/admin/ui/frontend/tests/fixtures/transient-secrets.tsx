import { render } from '@testing-library/react';
import { vi } from 'vitest';
import type { PendingSecretRequest, TransientSecretRequest } from '../../src/api/secrets';
import { SecretsPage } from '../../src/pages/SecretsPage';

export const connection = { id: 'transient-fixture', name: 'Commander', url: 'https://commander.example.test' };
export const credentials = { username: 'operator', url: connection.url };

export function transientRequest(suffix = '1'): TransientSecretRequest {
  const now = Date.now();
  return {
    id: `10000000-0000-4000-8000-${suffix.padStart(12, '0')}`,
    type: 'transient', name: 'CODE', agent_id: 'browser-agent-00000001', agent_type: 'browser-agent',
    operation_id: '20000000-0000-4000-8000-000000000001', task_id: '30000000-0000-4000-8000-000000000001',
    command_id: '40000000-0000-4000-8000-000000000001', requested_at: new Date(now - 1000).toISOString(),
    expires_at: new Date(now + 60_000).toISOString(), status: 'pending',
  };
}

export function reusableRequest(): PendingSecretRequest {
  const request = transientRequest('3');
  return { id: request.id, name: request.name, agent_id: request.agent_id, agent_type: request.agent_type,
    requested_at: request.requested_at, expires_at: request.expires_at, status: 'needs_value' };
}

export function view() {
  return render(<SecretsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
}

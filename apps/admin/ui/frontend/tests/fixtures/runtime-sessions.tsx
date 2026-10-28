import { fireEvent, render, screen } from '@testing-library/react';
import { vi } from 'vitest';
import type { RuntimeSession } from '../../src/api/runtime-sessions';
import { SessionsPage } from '../../src/pages/SessionsPage';
import { fixture as fakeAPI } from '../fakes/runtime-sessions';

export const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };

export const credentials = { username: 'operator', url: connection.url };

export const session: RuntimeSession = {
  id: '12345678-1234-4234-8234-123456789abc', scope_id: '22345678-1234-4234-8234-123456789abc',
  owner_agent_id: 'shell-agent-12345678', kind: 'shell', label: 'Development host', state: 'connected', disposition: 'active',
  endpoint: 'ssh://host.example.test', peer: 'host.example.test:22', hostname: 'dev-host', user: 'operator',
  busy: true, revision: 3, created_at: '2026-10-01T10:00:00Z', updated_at: '2026-10-02T10:00:00Z',
  connected_at: '2026-10-01T10:01:00Z', verified_at: '2026-10-02T10:00:00Z',
};

export const released: RuntimeSession = {
  ...session, id: '32345678-1234-4234-8234-123456789abc', label: 'Old resource', state: 'lost',
  disposition: 'released', release_reason: 'abandoned', busy: false, hostname: 'retired-host',
};

export function view(selectedID?: string) {
  return render(<SessionsPage connection={connection} credentials={credentials} selectedID={selectedID}
    onSelect={selectedID ? vi.fn() : undefined} onSignIn={vi.fn()} />);
}

export async function openControl(name: string) {
  fireEvent.click(await screen.findByRole('button', { name }));
  return screen.findByRole('dialog', { name });
}

export function fixture(respond?: Parameters<typeof fakeAPI>[1]) {
  return fakeAPI({ session, released }, respond);
}

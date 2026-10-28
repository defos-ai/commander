import { fireEvent, screen, within } from '@testing-library/react';
import { CONNECTIONS_STORAGE_KEY } from '../../src/connections/storage';
import type { Connection } from '../../src/connections/types';
import { mockAPI as fakeAPI } from '../fakes/app';

export const localURL = 'http://localhost:4274';

export const customLocalURL = 'http://localhost:14274';

export const remoteURL = 'https://api.example.test/commander';

export const username = ' operator ';

export const password = ' exact secret ';

export const savedConnection = { id: 'existing', name: 'Remote Commander', url: remoteURL };

export function persist(connections: Connection[]) {
  localStorage.setItem(CONNECTIONS_STORAGE_KEY, JSON.stringify(connections));
}

export function stored(): Connection[] {
  return JSON.parse(localStorage.getItem(CONNECTIONS_STORAGE_KEY) ?? '[]') as Connection[];
}

export function fillConnection({ name = 'Remote Commander', url = remoteURL } = {}) {
  const dialog = screen.getByRole('dialog');
  fireEvent.change(within(dialog).getByLabelText('Connection name'), { target: { value: name } });
  fireEvent.change(within(dialog).getByLabelText('API URL'), { target: { value: url } });
  fireEvent.change(within(dialog).getByLabelText('Username'), { target: { value: username } });
  fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: password } });
  return dialog;
}

export function connectionLink(url = localURL, user = 'commander', operation?: string) {
  return `#/connect?${new URLSearchParams({ api: url, username: user, ...(operation ? { operation } : {}) })}`;
}

export function mockAPI() {
  return fakeAPI({ localURL });
}

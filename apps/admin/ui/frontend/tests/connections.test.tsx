import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { ConnectionsPage } from '../src/pages/ConnectionsPage';

it('shows an empty state before any connection is configured', () => {
  render(<ConnectionsPage />);

  expect(screen.getByText('No connections yet')).toBeVisible();
  expect(screen.queryByRole('button')).not.toBeInTheDocument();
  expect(screen.getByText('0 connections')).toBeVisible();
});

it('opens the selected connection without treating unchecked APIs as healthy', () => {
  const onOpen = vi.fn();
  const onAdd = vi.fn();
  render(<ConnectionsPage connections={[
    { id: 'local', name: 'Local Commander', url: 'http://localhost:4274' },
    { id: 'remote', name: 'Remote Commander', url: 'https://commander.example.com' },
  ]} statuses={{ remote: 'authentication' }} onOpen={onOpen} onAdd={onAdd} />);

  fireEvent.click(screen.getByRole('button', { name: 'Open Remote Commander' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add connection' }));

  expect(onOpen).toHaveBeenCalledExactlyOnceWith('remote');
  expect(onAdd).toHaveBeenCalledOnce();
  expect(screen.getByText('Not checked')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Open Local Commander' })).toHaveAccessibleDescription('http://localhost:4274 Not checked');
  expect(screen.getByRole('button', { name: 'Open Remote Commander' })).toHaveAccessibleDescription('https://commander.example.com Sign in required');
  expect(screen.getByText('Sign in required')).toBeVisible();
  expect(screen.queryByText('Connected')).not.toBeInTheDocument();
});

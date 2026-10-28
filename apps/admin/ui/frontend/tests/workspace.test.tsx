import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { viewport } from './fakes/viewport';
import { connection, workspace } from './fixtures/workspace';

afterEach(() => vi.unstubAllGlobals());

describe('workspace navigation', () => {
  it('shows the connection and routes desktop navigation and back actions', () => {
    viewport(false);
    const { onNavigate, onBack } = workspace();

    expect(screen.getByText('COMMANDER')).toBeVisible();
    expect(screen.getByText(connection.name)).toBeVisible();
    expect(screen.getByText(connection.url)).toBeVisible();
    const navigation = screen.getByRole('navigation', { name: 'Workspace navigation' });
    expect(within(navigation).getAllByRole('button').map(button => button.textContent)).toEqual([
      'Overview', 'Operations', 'Missions', 'Controllers', 'Agents', 'Sessions', 'Actions', 'Connection settings', 'All connections',
    ]);
    expect(screen.getByRole('complementary')).toContainElement(screen.getByText(connection.name));
    expect(screen.getByRole('main')).not.toContainElement(screen.getByText(connection.name));
    expect(screen.getByRole('heading', { name: 'Connection overview' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Overview' })).toHaveAttribute('aria-current', 'page');
    expect(screen.queryByRole('button', { name: 'Open navigation' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Close navigation' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Operations' }));
    fireEvent.click(screen.getByRole('button', { name: 'All connections' }));

    expect(onNavigate).toHaveBeenCalledExactlyOnceWith('operations');
    expect(onBack).toHaveBeenCalledOnce();
  });

  it('opens a mobile drawer and contains keyboard focus and background scrolling', () => {
    viewport(true);
    const previousOverflow = document.body.style.overflow;
    const { unmount } = workspace();
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Open navigation' }));

    const dialog = screen.getByRole('dialog', { name: 'Workspace navigation' });
    const close = within(dialog).getByRole('button', { name: 'Close navigation' });
    const last = within(dialog).getByRole('button', { name: 'All connections' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(close).toHaveFocus();
    expect(screen.getByRole('main', { hidden: true })).toHaveAttribute('inert');
    expect(screen.queryByRole('button', { name: 'Open navigation' })).not.toBeInTheDocument();
    expect(document.body.style.overflow).toBe('hidden');

    fireEvent.keyDown(close, { key: 'Tab', shiftKey: true });
    expect(last).toHaveFocus();
    fireEvent.keyDown(last, { key: 'Tab' });
    expect(close).toHaveFocus();
    screen.getByRole('button', { name: 'Refresh overview', hidden: true }).focus();
    expect(close).toHaveFocus();

    unmount();
    expect(document.body.style.overflow).toBe(previousOverflow);
  });

  it.each(['Escape', 'close button'] as const)('closes with %s and restores the menu button focus', async method => {
    viewport(true);
    workspace();
    const trigger = screen.getByRole('button', { name: 'Open navigation' });
    trigger.focus();
    fireEvent.click(trigger);

    if (method === 'Escape') fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    else fireEvent.click(screen.getByRole('button', { name: 'Close navigation' }));

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
    expect(screen.getByRole('main')).not.toHaveAttribute('inert');
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it('closes mobile navigation after choosing a section', async () => {
    viewport(true);
    const { onNavigate } = workspace();
    fireEvent.click(screen.getByRole('button', { name: 'Open navigation' }));

    fireEvent.click(screen.getByRole('button', { name: 'Missions' }));

    expect(onNavigate).toHaveBeenCalledExactlyOnceWith('missions');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open navigation' })).toHaveFocus());
  });

  it('closes mobile navigation when returning to all connections', async () => {
    viewport(true);
    const { onBack } = workspace();
    fireEvent.click(screen.getByRole('button', { name: 'Open navigation' }));

    fireEvent.click(screen.getByRole('button', { name: 'All connections' }));

    expect(onBack).toHaveBeenCalledOnce();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Open navigation' })).toHaveFocus());
  });

  it('restores desktop navigation when resized and starts collapsed on mobile again', async () => {
    const resize = viewport(true);
    workspace();
    fireEvent.click(screen.getByRole('button', { name: 'Open navigation' }));

    resize(false);

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('navigation', { name: 'Workspace navigation' })).toBeVisible();
    expect(screen.getByRole('main')).not.toHaveAttribute('inert');
    expect(screen.queryByRole('button', { name: 'Open navigation' })).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Overview' })).toHaveFocus());

    resize(true);

    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open navigation' })).toHaveAttribute('aria-expanded', 'false');
  });
});

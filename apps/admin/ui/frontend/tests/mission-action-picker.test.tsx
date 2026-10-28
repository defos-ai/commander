import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Action, Agent } from '../src/api/types';
import { upstreamURL } from './fakes/proxy';
import { capture, links, Picker } from './fixtures/mission-action-picker';

describe('mission action picker', () => {
  it('searches action descriptions, inspects readiness and schemas, and keeps starting actions required', async () => {
    const fetch = vi.fn(async (input: RequestInfo | URL) => Response.json({ items: upstreamURL(input).endsWith('/actions') ? [capture, links] : [] }));
    vi.stubGlobal('fetch', fetch);
    const onChange = vi.fn();
    render(<Picker initial={{ requiredActions: ['uninstalled-action'], optionalActions: [], startingActions: [] }} onChange={onChange} />);
    expect(await screen.findByText('Not installed')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Role for uninstalled-action' })).toHaveValue('required');
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('checkbox', { name: 'Start with capture-page' })).toBeDisabled();
    expect(screen.getAllByText('Chromium is missing.')[0]).toBeVisible();

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search actions' }), { target: { value: 'SCREENSHOT' } });
    expect(screen.queryByRole('combobox', { name: 'Role for check-links' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Inspect capture-page'));
    expect(screen.getByText('Page to capture')).toBeVisible();
    const role = screen.getByRole('combobox', { name: 'Role for capture-page' });
    fireEvent.change(role, { target: { value: 'required' } });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Start with capture-page' }));
    expect(onChange).toHaveBeenLastCalledWith({ requiredActions: ['uninstalled-action', 'capture-page'], optionalActions: [], startingActions: ['capture-page'] });
    fireEvent.change(role, { target: { value: 'optional' } });
    expect(screen.getByRole('checkbox', { name: 'Start with capture-page' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Start with capture-page' })).toBeDisabled();
    expect(onChange).toHaveBeenLastCalledWith({ requiredActions: ['uninstalled-action'], optionalActions: ['capture-page'], startingActions: [] });
    fireEvent.change(role, { target: { value: 'none' } });
    expect(onChange).toHaveBeenLastCalledWith({ requiredActions: ['uninstalled-action'], optionalActions: [], startingActions: [] });
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: '' } });
    expect(screen.getByRole('combobox', { name: 'Role for uninstalled-action' })).toHaveValue('required');
    expect(fetch.mock.calls).toHaveLength(2);
  });

  it('keeps the draft editable during catalog failure and retry without exposing server errors', async () => {
    let failed = true;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => failed
      ? Response.json({ error: 'private-database-detail' }, { status: 503 })
      : Response.json({ items: upstreamURL(input).endsWith('/actions') ? [capture] : [] })));
    const onChange = vi.fn();
    render(<Picker initial={{ requiredActions: ['check-links'], optionalActions: [], startingActions: ['check-links'] }} onChange={onChange} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Your selections are kept; you can still save this mission.');
    expect(screen.queryByText('private-database-detail')).not.toBeInTheDocument();
    expect(screen.queryByText('Not installed')).not.toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Start with check-links' })).toBeChecked();
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole('combobox', { name: 'Role for check-links' }), { target: { value: 'optional' } });
    expect(onChange).toHaveBeenLastCalledWith({ requiredActions: [], optionalActions: ['check-links'], startingActions: [] });

    failed = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('Not installed')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Role for check-links' })).toHaveValue('optional');
    expect(screen.getByRole('combobox', { name: 'Role for capture-page' })).toHaveValue('none');
    expect(onChange).toHaveBeenCalledOnce();
  });

  it('offers local sign-in after expiry while preserving locked action selections', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 401 })));
    const onChange = vi.fn();
    const onSignIn = vi.fn();
    render(<Picker initial={{ requiredActions: ['private-action'], optionalActions: [], startingActions: ['private-action'] }}
      disabled onChange={onChange} onSignIn={onSignIn} />);
    const notice = await screen.findByRole('alert');
    fireEvent.click(within(notice).getByRole('button', { name: 'Sign in' }));
    expect(onSignIn).toHaveBeenCalledOnce();
    expect(screen.getByRole('combobox', { name: 'Role for private-action' })).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: 'Start with private-action' })).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: 'Start with private-action' })).toBeChecked();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('inspects individual agent reports without treating catalog definitions or stale reports as global readiness', async () => {
    const definition: Action = { ...capture, status: 'unknown', status_reason: undefined, missing_requirements: [] };
    const reported: Agent = {
      id: 'browser-agent-12345678', type: 'browser-agent', status: 'working', stale: false,
      actions: [{ ...definition, status: 'ready' }], server: { hostname: 'browser', ip_address: '127.0.0.1' },
      responsibilities: [], discovery_scope: null, config: {},
      last_seen: '2026-10-22T10:00:00Z', last_heartbeat: '2026-10-22T10:00:00Z',
    };
    const stale: Agent = { ...reported, id: 'browser-agent-87654321', status: 'unavailable', stale: true, actions: [capture] };
    let failed = true;
    const fetch = vi.fn(async (input: RequestInfo | URL, _init: RequestInit = {}) => {
      if (upstreamURL(input).endsWith('/actions')) return Response.json({ items: [definition] });
      return failed ? Response.json({ error: 'private-agent-store-detail' }, { status: 503 })
        : Response.json({ items: [reported, stale] });
    });
    vi.stubGlobal('fetch', fetch);
    render(<Picker />);
    fireEvent.click(await screen.findByText('Inspect capture-page'));
    expect(await screen.findByText('Agent reports could not be loaded. Refresh actions to try again.')).toBeVisible();
    expect(screen.queryByText('Unknown')).not.toBeInTheDocument();
    expect(screen.queryByText('private-agent-store-detail')).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('combobox', { name: 'Role for capture-page' }), { target: { value: 'required' } });

    failed = false;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh actions' }));
    fireEvent.click(await screen.findByText('Inspect capture-page'));
    const agentReport = (await screen.findByText(reported.id)).closest('li')!;
    expect(within(agentReport).getByText('Agent status')).toBeVisible();
    expect(within(agentReport).getByText('Working')).toBeVisible();
    expect(within(agentReport).getByText('Reported action readiness')).toBeVisible();
    expect(within(agentReport).getByText('Ready')).toBeVisible();
    const staleReport = screen.getByText(stale.id).closest('li')!;
    expect(within(staleReport).getByText('Last report; this agent’s heartbeat has expired.')).toBeVisible();
    expect(within(staleReport).getByText('Chromium is missing.')).toBeVisible();
    expect(within(staleReport).getByText('chromium')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Role for capture-page' })).toHaveValue('required');
    expect(fetch.mock.calls.every(([, init]) => init?.method === 'GET')).toBe(true);
  });
});

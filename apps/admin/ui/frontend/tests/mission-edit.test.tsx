import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { EditMissionPage } from '../src/pages/EditMissionPage';
import { MissionsPage } from '../src/pages/MissionsPage';
import { upstreamURL } from './fakes/proxy';
import { deferredResponse } from './fixtures/deferred';
import { connection, credentials, mission, mockAPI, mockEditableMission } from './fixtures/missions';

describe('mission editing', () => {
  it('edits objectives, action roles and details while preserving the name and untouched schema tokens', async () => {
    const name = 'Review / #1?';
    const definition = String.raw`{"name":"Review / #1?","objectives":["Review the service."],"required_actions":["discover-pages"],"optional_actions":["capture-page"],"starting_actions":["discover-pages"],"input_schema":{"type":"object","properties":{"id":{"const":9007199254740993},"path":{"const":"\u0061\\path"}}},"output_schema":{}}`;
    const fetch = mockEditableMission(definition);
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} selectedID={name} onSelect={vi.fn()} />);

    fireEvent.click(await screen.findByRole('button', { name: 'Edit mission' }));
    expect(await screen.findByRole('textbox', { name: 'Mission name' })).toHaveValue(name);
    expect(screen.getByRole('textbox', { name: 'Mission name' })).toHaveAttribute('readonly');
    fireEvent.change(screen.getByRole('textbox', { name: 'Objective 1' }), { target: { value: 'Check release health.' } });
    fireEvent.change(await screen.findByRole('combobox', { name: 'Role for capture-page' }), { target: { value: 'required' } });
    fireEvent.click(screen.getByText('Advanced settings'));
    fireEvent.change(screen.getByRole('textbox', { name: 'Output schema (JSON)' }), { target: { value: '{"type":"object","properties":{"summary":{"type":"string"}}}' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByRole('heading', { name, level: 1 })).toBeVisible();
    expect(await screen.findByText('Check release health.')).toBeVisible();
    const writes = fetch.mock.calls.filter(([, init]) => init?.method === 'PUT');
    expect(writes).toHaveLength(1);
    expect(upstreamURL(writes[0][0])).toBe(`${connection.url}/api/v1/missions/${encodeURIComponent(name)}`);
    const body = writes[0][1]!.body as string;
    expect(body).toContain('9007199254740993');
    expect(body).toContain(String.raw`"\u0061\\path"`);
    expect(JSON.parse(body)).toMatchObject({ name, objectives: ['Check release health.'], required_actions: ['discover-pages', 'capture-page'],
      optional_actions: [], output_schema: { type: 'object', properties: { summary: { type: 'string' } } } });
    expect(fetch.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false);
  });

  it('rejects a changed name in JSON before writing or showing the renamed value in the form', async () => {
    const fetch = mockEditableMission();
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} selectedID={mission.name} onSelect={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Edit mission' }));
    fireEvent.click(await screen.findByRole('button', { name: 'JSON' }));
    const editor = screen.getByRole('textbox', { name: 'Mission definition (JSON)' });
    const renamed = JSON.stringify({ ...mission, name: 'another-mission' });

    fireEvent.change(editor, { target: { value: renamed } });
    fireEvent.click(screen.getByRole('button', { name: 'Form' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('The mission name cannot be changed.');
    expect(editor).toHaveValue(renamed);
    expect(screen.queryByRole('textbox', { name: 'Mission name' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('The mission name cannot be changed.');
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(0);
  });

  it('keeps an uncertain update locked until the operator inspects saved state without retrying', async () => {
    const fetch = mockEditableMission(JSON.stringify(mission), true);
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} selectedID={mission.name} onSelect={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Edit mission' }));
    fireEvent.change(await screen.findByRole('textbox', { name: 'Objective 1' }), { target: { value: 'The server accepted this update.' } });

    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('The save result is unknown.');
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
    expect(screen.queryByText('private-update-response-failed')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Check saved mission' }));
    expect(await screen.findByText('The server accepted this update.')).toBeVisible();
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
  });

  it('cancels a pending update and ignores its late response after the edit page is closed', async () => {
    const pending = deferredResponse();
    let signal: AbortSignal | undefined;
    mockAPI((_url, init) => {
      if (init.method === 'PUT') { signal = init.signal ?? undefined; return pending.promise; }
      return _url.endsWith('/api/v1/agents') ? Response.json({ items: [] }) : Response.json(mission);
    });
    const onBack = vi.fn();
    const onSelect = vi.fn();
    const view = render(<EditMissionPage connection={connection} credentials={credentials} onSignIn={vi.fn()}
      name={mission.name} onBack={onBack} onSelect={onSelect} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(signal).toBeDefined());

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    view.unmount();
    await act(async () => pending.resolve(Response.json(mission)));

    expect(signal?.aborted).toBe(true);
    expect(onBack).toHaveBeenCalledOnce();
    expect(onSelect).not.toHaveBeenCalled();
  });
});

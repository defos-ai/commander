import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { App } from '../src/App';
import { CONNECTIONS_STORAGE_KEY } from '../src/connections/storage';
import { parseRoute, routeHash, type Route } from '../src/navigation/routes';
import { upstreamURL } from './fakes/proxy';
import { connection, mission, mockAPI, operation, operationWithLogs, resourceSession, visit } from './fixtures/navigation';

async function signIn() {
  fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
  const dialog = screen.getByRole('dialog', { name: 'Edit connection' });
  fireEvent.change(within(dialog).getByLabelText('Username'), { target: { value: 'operator' } });
  fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: 'private-password' } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Save connection' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
}

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem(CONNECTIONS_STORAGE_KEY, JSON.stringify([connection]));
});

describe('location navigation', () => {
  it('round-trips entity names and IDs without treating their punctuation as route segments', () => {
    const logOperation = operationWithLogs();
    const routes: Route[] = [{}, { connect: { url: 'http://127.0.0.1:4274', username: 'commander' } },
      { connect: { url: 'http://127.0.0.1:4274', username: 'commander', operation: 'Résumé / a?#b' } },
      { operation: 'Résumé / a?#b' },
      { connectionID: 'local', section: 'overview' }, { connectionID: 'local', section: 'settings' },
      { connectionID: 'local', section: 'secrets' }, { connectionID: 'local', section: 'create-mission' },
      { connectionID: 'local', section: 'sessions' },
      { connectionID: 'local', section: 'sessions', itemID: resourceSession.id,
        sessionFilters: { scope_id: resourceSession.scope_id, owner_agent_id: resourceSession.owner_agent_id } },
      { connectionID: 'local', section: 'missions', itemID: 'create-mission' },
      ...(['missions', 'controllers', 'actions', 'agents', 'operations'] as const).map(section => ({ connectionID: 'private/team', section, itemID: 'Résumé / a?#b' })),
      { connectionID: 'local', section: 'operations', itemID: operation.id, view: 'graph' },
      { connectionID: 'local', section: 'operations', itemID: logOperation.id, view: 'logs' },
      { connectionID: 'local', section: 'operations', itemID: logOperation.id, view: 'logs', logFilters: {
        task_id: logOperation.tasks![0].id, agent_id: 'worker-agent-12345678', action_name: 'Inspect / page?mode=full#latest ' } },
      { connectionID: 'private/team', section: 'missions', itemID: 'Résumé / a?#b', view: 'json' },
      { connectionID: 'private/team', section: 'missions', itemID: 'Résumé / a?#b', view: 'edit' }];

    for (const route of routes) expect(parseRoute(routeHash(route))).toEqual(route);
    expect(routeHash({ connectionID: 'local', section: 'operations', itemID: operation.id, view: 'details' }))
      .toBe(`#/connections/local/operations/${operation.id}`);
    expect(routeHash({ connectionID: 'local', section: 'missions', itemID: mission.name, view: 'details' }))
      .toBe(`#/connections/local/missions/${mission.name}`);
    for (const hash of ['#/%', '#/connections/id/unknown', '#/connections/id/overview/item', '#/connections/id/secrets/item', '#/connections/id/create-mission/item', '#/connections/id/create-mission/', '#/connections/id/operations/',
      '#/connections/id/missions/item/graph', '#/connections/id/operations/item/json', '#/connections/id/missions/item/json/extra',
      '#/connections/id/create-mission/item/graph', '#/connections/id/operations/item/unknown', '#/connections/id/agents/item/graph', '#/connections/id/operations/item/graph/extra',
      '#/connections/id/sessions/item/graph', '#/connections/id/sessions?token=secret', '#/connections/id/sessions?scope_id=',
      '#/connections/id/sessions?owner_agent_id=a&owner_agent_id=b', '#/connections/id/agents?owner_agent_id=a',
      '#/connections/id/operations/item/logs?token=secret', '#/connections/id/operations/item/logs?task_id=',
      '#/connections/id/operations/item/logs?agent_id=a&agent_id=b', '#/connections/id/operations/item/logs?action_name=',
      '#/connections/id/operations/item/logs?action_name=%', '#/connections/id/operations/item/logs?action_name=%FF',
      '#/connections/id/operations/item/logs?order=desc', '#/connections/id/operations/item/logs/extra',
      '#/connections/id/operations/item?task_id=a', '#/connections/id/operations/item/graph?task_id=a',
      '#/connections/id/missions/item/logs', '#/connections/id/agents/item/logs',
      '#/operations/', '#/operations/id/extra', '#/operations/id?token=secret', '#/operations/id#fragment',
      '#/operations/%0A', '#/operations/%', '#/operations/' + 'a'.repeat(257)]) {
      expect(parseRoute(hash)).toEqual({});
    }
  });

  it('keeps Sessions details and owner/scope filters through sign-in, list navigation, history and reload', async () => {
    const fetch = mockAPI();
    const filters = { scope_id: resourceSession.scope_id, owner_agent_id: resourceSession.owner_agent_id };
    visit({ connectionID: connection.id, section: 'sessions', itemID: resourceSession.id, sessionFilters: filters });
    const first = render(<App />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Sign in to view sessions');
    await signIn();
    expect(await screen.findByText(resourceSession.label)).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Sessions', level: 1 })).toBeVisible();
    const navigation = screen.getByRole('navigation', { name: 'Workspace navigation' });
    expect(within(navigation).getByRole('button', { name: 'Sessions' })).toHaveAttribute('aria-current', 'page');
    fireEvent.click(screen.getByRole('button', { name: 'All sessions' }));
    const row = await screen.findByRole('button', { name: `View session ${resourceSession.id}` });
    expect(window.location.hash).toBe(routeHash({ connectionID: connection.id, section: 'sessions', sessionFilters: filters }));
    expect(screen.getByLabelText('Scope ID')).toHaveValue(filters.scope_id);
    expect(screen.getByLabelText('Owner agent ID')).toHaveValue(filters.owner_agent_id);
    fireEvent.click(row);
    expect(await screen.findByRole('heading', { name: 'Saved session' })).toBeVisible();
    act(() => { window.history.back(); });
    await screen.findByRole('button', { name: `View session ${resourceSession.id}` });
    act(() => { window.history.forward(); });
    await screen.findByRole('heading', { name: 'Saved session' });
    first.unmount();
    render(<App />);
    expect(await screen.findByText(resourceSession.label)).toBeVisible();
    expect(parseRoute(window.location.hash)).toEqual({ connectionID: connection.id, section: 'sessions', itemID: resourceSession.id, sessionFilters: filters });
    expect(fetch.mock.calls.every(([, init]) => !new Headers(init?.headers).has('Authorization'))).toBe(true);
    expect(window.location.href).not.toContain('private-password');
  });

  it.each([
    '', 'api=https://api.example.test', 'username=commander',
    'api=https://api.example.test&username=commander&password=secret',
    'api=https://api.example.test&username=commander&token=secret',
    'api=https://api.example.test&username=commander&return=https://other.test',
    'api=https://api.example.test&api=https://other.test&username=commander',
    'api=https://api.example.test&username=one&username=two',
    'api=https://api.example.test&username=one&operation=a&operation=b',
    ...['', ' ', '\n', 'a'.repeat(257)].map(operation => new URLSearchParams({ api: 'https://api.example.test', username: 'commander', operation }).toString()),
    'api=https://api.example.test&username=%',
    ...['javascript:alert(1)', '/api', 'https://user:secret@api.example.test',
      'https://api.example.test?token=secret', 'https://api.example.test#secret',
      'https://api.example.test\n', 'https://api.example.test\\extra', 'https://api.example.test/' + 'a'.repeat(2048)]
      .map(api => new URLSearchParams({ api, username: 'commander' }).toString()),
    ...['', ' ', 'user:secret', 'user\n', '\u007f', 'a'.repeat(257)]
      .map(username => new URLSearchParams({ api: 'https://api.example.test', username }).toString()),
  ])('rejects invalid connection link fields: %s', query => {
    expect(parseRoute(`#/connect?${query}`)).toEqual({ connect: null });
  });

  it('restores operation details and graph through browser history and restores the server session after reload', async () => {
    const fetch = mockAPI();
    visit({ connectionID: connection.id, section: 'overview' });
    const first = render(<App />);
    await screen.findByRole('button', { name: 'Sign in' });
    await signIn();

    fireEvent.click(screen.getByRole('button', { name: 'Operations' }));
    fireEvent.click(await screen.findByRole('button', { name: `View operation ${operation.id}` }));
    fireEvent.click(await screen.findByRole('button', { name: 'Execution graph' }));
    expect(await screen.findByText('No work recorded yet')).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/operations/${operation.id}/graph`);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel operation' }));
    expect(screen.getByRole('dialog')).toBeVisible();

    act(() => { window.history.back(); });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Details' })).toHaveAttribute('aria-pressed', 'true'));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    act(() => { window.history.back(); });
    expect(await screen.findByRole('heading', { name: 'Operations', level: 1 })).toBeVisible();
    act(() => { window.history.forward(); });
    await screen.findByRole('heading', { name: operation.id, level: 1 });
    act(() => { window.history.forward(); });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Execution graph' })).toHaveAttribute('aria-pressed', 'true'));

    first.unmount();
    fetch.mockClear();
    render(<App />);
    expect(await screen.findByRole('heading', { name: operation.id, level: 1 })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Sign in' })).not.toBeInTheDocument();
    expect(fetch.mock.calls.every(([, init]) => !new Headers(init?.headers).has('Authorization'))).toBe(true);
    expect(await screen.findByText('No work recorded yet')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Execution graph' })).toHaveAttribute('aria-pressed', 'true');
    expect(window.location.href).not.toContain('private-password');
    expect(window.location.hash).not.toContain(connection.url);
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).not.toContain('private-password');
    expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
  });

  it('keeps operation log filters through sign-in, task links, view switches, history and reload', async () => {
    const snapshot = operationWithLogs();
    const task = snapshot.tasks![0];
    const filters = { task_id: task.id, agent_id: task.agent_id, action_name: task.action_name };
    const logsRoute: Route = { connectionID: connection.id, section: 'operations', itemID: snapshot.id, view: 'logs', logFilters: filters };
    const fetch = mockAPI(snapshot);
    const logRequests = () => fetch.mock.calls.map(([url]) => upstreamURL(url)).filter(url => url.includes(`/operations/${snapshot.id}/logs?`));
    visit(logsRoute);
    const first = render(<App />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Sign in to view operations');
    expect(screen.queryByRole('heading', { name: 'Logs' })).not.toBeInTheDocument();
    expect(logRequests()).toHaveLength(0);

    await signIn();

    expect(await screen.findByRole('heading', { name: 'Logs', level: 2 })).toBeVisible();
    expect(screen.getByLabelText('Task')).toHaveValue(task.id);
    expect(screen.getByLabelText('Agent ID')).toHaveValue(filters.agent_id);
    expect(screen.getByLabelText('Action')).toHaveValue(filters.action_name);
    await waitFor(() => {
      const request = new URL(logRequests().at(-1)!);
      expect(request.searchParams.get('task_id')).toBe(task.id);
      expect(request.searchParams.get('agent_id')).toBe(filters.agent_id);
      expect(request.searchParams.get('action_name')).toBe(filters.action_name);
    });
    expect(window.location.hash).toBe(routeHash(logsRoute));
    expect(screen.queryByRole('button', { name: 'Refresh' })).not.toBeInTheDocument();
    expect(screen.queryByText(/Snapshots refresh every 10 seconds/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel operation' })).toBeVisible();
    expect(within(screen.getByRole('navigation', { name: 'Workspace navigation' })).queryByRole('button', { name: 'Logs' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Details' }));
    expect(await screen.findByRole('heading', { name: 'Operation summary' })).toBeVisible();
    fetch.mockClear();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(fetch.mock.calls.some(([url]) => upstreamURL(url).endsWith(`/operations/${snapshot.id}`))).toBe(true));
    expect(logRequests()).toHaveLength(0);
    fireEvent.click(screen.getByText(task.action_name, { selector: 'summary *' }).closest('summary')!);

    fireEvent.click(screen.getByRole('button', { name: 'View logs' }));

    expect(await screen.findByRole('heading', { name: 'Logs', level: 2 })).toBeVisible();
    expect(screen.getByLabelText('Task')).toHaveValue(task.id);
    expect(screen.getByLabelText('Agent ID')).toHaveValue('');
    expect(screen.getByLabelText('Action')).toHaveValue('');
    expect(parseRoute(window.location.hash)).toEqual({ ...logsRoute, logFilters: { task_id: task.id } });
    fireEvent.change(screen.getByLabelText('Agent ID'), { target: { value: filters.agent_id } });
    fireEvent.change(screen.getByLabelText('Action'), { target: { value: filters.action_name } });

    fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }));

    expect(window.location.hash).toBe(routeHash(logsRoute));
    fetch.mockClear();
    fireEvent.click(screen.getByRole('button', { name: 'Execution graph' }));
    expect(await screen.findByText('No work recorded yet')).toBeVisible();
    expect(logRequests()).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Logs' }));
    expect(await screen.findByRole('heading', { name: 'Logs', level: 2 })).toBeVisible();
    expect(screen.getByLabelText('Task')).toHaveValue(task.id);
    expect(screen.getByLabelText('Agent ID')).toHaveValue(filters.agent_id);
    expect(screen.getByLabelText('Action')).toHaveValue(filters.action_name);
    expect(window.location.hash).toBe(routeHash(logsRoute));
    act(() => { window.history.back(); });
    expect(await screen.findByText('No work recorded yet')).toBeVisible();
    act(() => { window.history.forward(); });
    expect(await screen.findByRole('heading', { name: 'Logs', level: 2 })).toBeVisible();
    expect(parseRoute(window.location.hash)).toEqual(logsRoute);
    first.unmount();
    fetch.mockClear();

    render(<App />);

    expect(await screen.findByRole('heading', { name: 'Logs', level: 2 })).toBeVisible();
    expect(screen.getByLabelText('Task')).toHaveValue(task.id);
    expect(screen.getByLabelText('Agent ID')).toHaveValue(filters.agent_id);
    expect(screen.getByLabelText('Action')).toHaveValue(filters.action_name);
    expect(parseRoute(window.location.hash)).toEqual(logsRoute);
    await waitFor(() => expect(logRequests().length).toBeGreaterThan(0));
  });

  it.each(['missions', 'controllers', 'agents', 'actions'] as const)('opens a saved %s detail location behind the sign-in gate', async section => {
    mockAPI();
    visit({ connectionID: connection.id, section, itemID: 'saved-item' });
    render(<App />);

    expect(await screen.findByRole('heading', { name: 'saved-item', level: 1 })).toBeVisible();
    expect(screen.getByRole('alert')).toHaveTextContent(`Sign in to view ${section}`);
    const hash = window.location.hash;
    fireEvent.click(screen.getByRole('link', { name: 'Skip to content' }));
    expect(screen.getByRole('main')).toHaveFocus();
    expect(window.location.hash).toBe(hash);
  });

  it('restores mission creation through sign-in, history and reload, then opens the saved mission', async () => {
    const fetch = mockAPI();
    visit({ connectionID: connection.id, section: 'create-mission' });
    const first = render(<App />);
    expect(await screen.findByRole('heading', { name: 'Create mission', level: 1 })).toBeVisible();
    expect(screen.queryByRole('textbox', { name: 'Mission definition (JSON)' })).not.toBeInTheDocument();
    const creationHash = window.location.hash;
    await signIn();

    let editor = screen.getByRole('textbox', { name: 'Mission name' });
    expect(editor).toHaveFocus();
    expect(window.location.hash).toBe(creationHash);
    expect(creationHash).toBe(`#/connections/${connection.id}/create-mission`);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(within(screen.getByRole('navigation', { name: 'Workspace navigation' })).getByRole('button', { name: 'Missions' }))
      .toHaveAttribute('aria-current', 'page');
    const savedConnections = localStorage.getItem(CONNECTIONS_STORAGE_KEY);
    fireEvent.change(editor, { target: { value: 'private-draft' } });
    fireEvent.click(screen.getByRole('button', { name: 'All missions' }));
    expect(await screen.findByRole('heading', { name: 'Missions', level: 1 })).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/missions`);
    act(() => { window.history.back(); });
    editor = await screen.findByRole('textbox', { name: 'Mission name' });
    expect(window.location.hash).toBe(creationHash);
    expect(editor).toHaveValue('');
    act(() => { window.history.forward(); });
    expect(await screen.findByRole('heading', { name: 'Missions', level: 1 })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
    expect(await screen.findByRole('heading', { name: 'Create mission', level: 1 })).toBeVisible();
    first.unmount();
    render(<App />);
    editor = await screen.findByRole('textbox', { name: 'Mission name' });
    expect(window.location.hash).toBe(creationHash);
    expect(editor).toHaveValue('');
    expect(localStorage.getItem(CONNECTIONS_STORAGE_KEY)).toBe(savedConnections);
    expect(JSON.stringify({ ...localStorage })).not.toContain('private-draft');
    expect(window.location.href).not.toContain('private-draft');
    expect(fetch.mock.calls.filter(([url, init]) => init?.method === 'POST' && upstreamURL(url).endsWith('/api/v1/missions'))).toHaveLength(0);

    const created = { ...mission, name: 'custom-review' };
    fireEvent.click(screen.getByRole('button', { name: 'JSON' }));
    editor = screen.getByRole('textbox', { name: 'Mission definition (JSON)' });
    fireEvent.change(editor, { target: { value: JSON.stringify(created) } });
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
    expect(await screen.findByRole('heading', { name: created.name, level: 1 })).toBeVisible();
    expect(await screen.findByRole('heading', { name: 'Starting actions' })).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/missions/${created.name}`);
    expect(fetch.mock.calls.filter(([url, init]) => init?.method === 'POST' && upstreamURL(url).endsWith('/api/v1/missions'))).toHaveLength(1);
    expect(fetch.mock.calls.some(([url, init]) => init?.method === 'GET' && upstreamURL(url).endsWith(`/api/v1/missions/${created.name}`))).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'All missions' }));
    expect(await screen.findByRole('button', { name: `View mission ${created.name}` })).toBeVisible();
  });

  it('keeps the mission edit route through sign-in and reload, discards cancelled drafts and opens fresh details after save', async () => {
    const fetch = mockAPI();
    visit({ connectionID: connection.id, section: 'missions', itemID: mission.name, view: 'edit' });
    const first = render(<App />);
    await screen.findByRole('button', { name: 'Sign in' });
    await signIn();
    expect(await screen.findByRole('heading', { name: 'Edit mission', level: 1 })).toBeVisible();
    const editHash = window.location.hash;
    expect(editHash).toBe(`#/connections/${connection.id}/missions/${mission.name}/edit`);
    expect(await screen.findByRole('textbox', { name: 'Mission name' })).toHaveAttribute('readonly');
    fireEvent.change(screen.getByRole('textbox', { name: 'Objective 1' }), { target: { value: 'Unsaved changes.' } });

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(await screen.findByRole('heading', { name: mission.name, level: 1 })).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/missions/${mission.name}`);
    fireEvent.click(screen.getByRole('button', { name: 'Edit mission' }));
    expect(await screen.findByRole('textbox', { name: 'Objective 1' })).toHaveValue(mission.objectives[0]);
    first.unmount();
    render(<App />);
    expect(await screen.findByRole('textbox', { name: 'Objective 1' })).toHaveValue(mission.objectives[0]);
    expect(window.location.hash).toBe(editHash);
    fireEvent.change(screen.getByRole('textbox', { name: 'Objective 1' }), { target: { value: 'Updated objectives.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByRole('heading', { name: mission.name, level: 1 })).toBeVisible();
    expect(await screen.findByText('Updated objectives.')).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/missions/${mission.name}`);
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
    expect(JSON.stringify({ ...localStorage })).not.toContain('Unsaved changes.');
  });

  it('restores the saved mission JSON view through sign-in, browser history and reload', async () => {
    const fetch = mockAPI();
    visit({ connectionID: connection.id, section: 'missions', itemID: mission.name, view: 'json' });
    const first = render(<App />);
    await screen.findByRole('button', { name: 'Sign in' });
    const jsonHash = window.location.hash;
    await signIn();
    expect(await screen.findByRole('textbox', { name: 'Mission definition (JSON)' })).toHaveAttribute('readonly');
    expect(screen.getByRole('button', { name: 'JSON' })).toHaveAttribute('aria-pressed', 'true');
    expect(window.location.hash).toBe(jsonHash);
    fireEvent.click(screen.getByRole('button', { name: 'Details' }));
    expect(await screen.findByRole('heading', { name: 'Starting actions' })).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/missions/${mission.name}`);
    act(() => { window.history.back(); });
    expect(await screen.findByRole('textbox', { name: 'Mission definition (JSON)' })).toBeVisible();
    expect(window.location.hash).toBe(jsonHash);
    act(() => { window.history.forward(); });
    expect(await screen.findByRole('heading', { name: 'Starting actions' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'JSON' }));
    first.unmount();
    fetch.mockClear();
    render(<App />);
    expect(await screen.findByRole('textbox', { name: 'Mission definition (JSON)' })).toBeVisible();
    expect(window.location.hash).toBe(jsonHash);
    expect(screen.getByRole('button', { name: 'JSON' })).toHaveAttribute('aria-pressed', 'true');
    expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
  });

  it('opens Secrets from operations and Agents on the same connection without sending a mutation', async () => {
    const fetch = mockAPI({ ...operation, status: 'waiting', preflight: { ready: false, version: 1, actions: [{
      action_name: 'build', unavailable_agents: [{ agent_id: 'builder-agent-1234abcd', agent_type: 'builder',
        reason: 'Action has missing requirements', missing_requirements: [{ kind: 'secret', name: 'TOKEN' }] }],
    }] } });
    localStorage.setItem(CONNECTIONS_STORAGE_KEY, JSON.stringify([connection, { id: 'other', name: 'Other API', url: 'https://other.example.test' }]));
    visit({ connectionID: connection.id, section: 'operations', itemID: operation.id });
    render(<App />);
    await screen.findByRole('button', { name: 'Sign in' });
    await signIn();

    const manage = await screen.findByRole('button', { name: 'Manage secrets' });
    fetch.mockClear();
    fireEvent.click(manage);

    expect(await screen.findByRole('heading', { name: 'Secrets', level: 1 })).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/secrets`);
    const navigation = within(screen.getByRole('navigation', { name: 'Workspace navigation' }));
    expect(navigation.queryByRole('button', { name: 'Secrets' })).not.toBeInTheDocument();
    expect(navigation.getByRole('button', { name: 'Agents' })).toHaveAttribute('aria-current', 'page');
    fireEvent.click(screen.getByRole('button', { name: 'All agents' }));
    expect(await screen.findByRole('heading', { name: 'Agents', level: 1 })).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/agents`);
    fireEvent.click(screen.getByRole('button', { name: 'Manage secrets' }));
    expect(await screen.findByRole('heading', { name: 'Secrets', level: 1 })).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/secrets`);
    act(() => { window.history.back(); });
    expect(await screen.findByRole('heading', { name: 'Agents', level: 1 })).toBeVisible();
    act(() => { window.history.forward(); });
    expect(await screen.findByRole('heading', { name: 'Secrets', level: 1 })).toBeVisible();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
  });

  it('falls back to connections when a saved connection is missing, without calling a URL from the location', async () => {
    const fetch = mockAPI();
    visit({ connectionID: 'https://untrusted.example', section: 'operations', itemID: operation.id });
    render(<App />);

    expect(screen.getByRole('heading', { name: 'Connections' })).toBeVisible();
    expect(screen.getByText('This connection is not saved in this browser. Choose or add a connection.')).toBeVisible();
    await act(async () => {});
    expect(fetch.mock.calls.every(([url]) => String(url).startsWith('/ui/'))).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Open Local Commander' }));
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeVisible();
    expect(window.location.hash).toBe(`#/connections/${connection.id}/overview`);
  });
});

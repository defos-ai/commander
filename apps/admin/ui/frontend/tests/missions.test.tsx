import { upstreamURL } from './proxy';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Action, Mission } from '../src/api/types';
import { MissionsPage } from '../src/pages/MissionsPage';
import { CreateMissionPage } from '../src/pages/CreateMissionPage';

const connection = { id: 'local', name: 'Local Commander', url: 'https://commander.example.test' };
const credentials = { username: 'operator', url: connection.url };
const mission: Mission = {
  name: 'review-website-with-browser',
  objectives: ['Review website links.', 'Assess page quality.'],
  required_actions: ['discover-pages', 'check-links'],
  optional_actions: ['capture-page'],
  starting_actions: ['discover-pages'],
  input_schema: {
    type: 'object', required: ['url'],
    properties: { url: { type: 'string', description: 'Website to review' }, max_pages: { type: 'integer', default: 3 } },
  },
  output_schema: { type: 'object', properties: { summary: { type: 'string' } } },
  rules: {
    'aggregate-results': { for_actions: ['discover-pages'] },
    'decide-next-actions': { for_actions: ['check-links', 'capture-page'], params: { decision_by: 'model' } },
    'same-agent': { for_actions: ['discover-pages', 'check-links'] },
  },
};
const otherMission: Mission = { ...mission, name: 'review-service', objectives: ['Check service availability.'] };
const registeredActions: Action[] = [
  { name: 'discover-pages', description: 'Discover website pages.', status: 'ready', input_schema: { type: 'object' }, output_schema: { type: 'object' } },
  { name: 'check-links', description: 'Check HTTP links.', status: 'ready', input_schema: { type: 'object' }, output_schema: { type: 'object' } },
  { name: 'capture-page', description: 'Capture a screenshot.', status: 'unavailable', status_reason: 'Browser is not configured.',
    input_schema: { type: 'object' }, output_schema: { type: 'object' } },
];

function mockAPI(respond?: (url: string, init: RequestInit) => Response | Promise<Response>) {
  const fetch = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    if (upstreamURL(input).endsWith('/api/v1/actions')) return Response.json({ items: registeredActions });
    if (respond) return respond(upstreamURL(input), init);
    return upstreamURL(input).endsWith('/api/v1/missions')
      ? Response.json({ items: [mission, otherMission] }) : Response.json(mission);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>(complete => { resolve = complete; });
  return { promise, resolve };
}

function creationJSONEditor() {
  fireEvent.click(screen.getByRole('button', { name: 'JSON' }));
  return screen.getByRole('textbox', { name: 'Mission definition (JSON)' });
}

describe('missions browser', () => {
  it('searches missions, opens their definition and preserves the search when returning', async () => {
    const fetch = mockAPI();
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('button', { name: 'View mission review-website-with-browser' })).toBeVisible();
    expect(screen.getByText('2 missions')).toBeVisible();
    const listedMission = within(screen.getByRole('button', { name: 'View mission review-website-with-browser' }));
    expect(listedMission.getByText('2 required actions')).toBeVisible();
    expect(listedMission.getByText('1 optional action')).toBeVisible();

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search missions' }), { target: { value: 'PAGE QUALITY' } });
    expect(screen.getByText('1 of 2 missions')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'View mission review-service' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'View mission review-website-with-browser' }));

    expect(await screen.findByRole('heading', { name: 'Starting actions' })).toBeVisible();
    expect(screen.getByRole('heading', { name: mission.name, level: 1 })).toHaveFocus();
    const objectives = screen.getByRole('heading', { name: 'Objectives' }).closest('section')!;
    expect(within(objectives).getByRole('list')).toHaveClass('catalog-tags');
    expect(within(objectives).getAllByRole('listitem').map(item => item.textContent)).toEqual(mission.objectives);
    expect(screen.getByText('Website to review')).toBeVisible();
    const optionalActions = screen.getByRole('heading', { name: 'Optional actions' }).closest('section')!;
    expect(within(optionalActions).getByText('capture-page')).toBeVisible();
    expect(within(optionalActions).queryByText('check-links')).not.toBeInTheDocument();
    const rules = within(screen.getByRole('table', { name: 'Mission rules' }));
    expect(rules.getAllByRole('row')).toHaveLength(4);
    expect(rules.getByRole('row', { name: 'Aggregate results discover-pages All direct children' })).toBeVisible();
    expect(rules.getByRole('row', { name: 'Same agent discover-pages check-links One agent per operation; no fallback' })).toBeVisible();
    expect(rules.getByRole('row', { name: 'Decide next actions check-links capture-page Model' })).toBeVisible();
    expect(fetch.mock.calls.some(([url]) => upstreamURL(url) === `${connection.url}/api/v1/missions/review-website-with-browser`)).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'All missions' }));

    expect(await screen.findByRole('searchbox', { name: 'Search missions' })).toHaveValue('PAGE QUALITY');
    expect(screen.getByRole('heading', { name: 'Missions', level: 1 })).toHaveFocus();
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'not in the catalog' } });
    expect(screen.getByRole('heading', { name: 'No matching missions' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
    expect(screen.getByRole('button', { name: 'View mission review-service' })).toBeVisible();
  });

  it('shows and copies the saved JSON without rounding schema numbers or changing string escapes', async () => {
    const definition = String.raw`{"name":"lossless-review","objectives":["Check \"quoted\" text."],"required_actions":[],"starting_actions":[],"input_schema":{"properties":{"id":{"const":9007199254740993},"path":{"const":"\u0061\\path"}}},"output_schema":{}}`;
    const fetch = mockAPI(() => new Response(definition, { headers: { 'Content-Type': 'application/json' } }));
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', Object.assign(Object.create(navigator), { clipboard: { writeText } }));
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} selectedID="lossless-review" onSelect={vi.fn()} />);
    await screen.findByRole('heading', { name: 'Starting actions' });
    const views = within(screen.getByRole('group', { name: 'Mission view' }));
    expect(views.getByRole('button', { name: 'Details' })).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(views.getByRole('button', { name: 'JSON' }));

    const editor = screen.getByRole('textbox', { name: 'Mission definition (JSON)' }) as HTMLTextAreaElement;
    expect(editor).toHaveAttribute('readonly');
    expect(editor).toBeEnabled();
    expect(editor.value).toContain('9007199254740993');
    expect(editor.value).toContain(String.raw`"\u0061\\path"`);
    expect(editor.value).toContain(String.raw`"Check \"quoted\" text."`);
    expect(editor.value).toMatch(/\n\s+"name":/);
    expect(views.getByRole('button', { name: 'JSON' })).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByRole('button', { name: 'Copy JSON' }));
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeVisible();
    expect(writeText).toHaveBeenCalledExactlyOnceWith(editor.value);
    expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
  });

  it('keeps saved JSON selectable when the clipboard is unavailable', async () => {
    mockAPI();
    vi.stubGlobal('navigator', Object.assign(Object.create(navigator), { clipboard: undefined }));
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} selectedID={mission.name} onSelect={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'JSON' }));
    const editor = screen.getByRole('textbox', { name: 'Mission definition (JSON)' }) as HTMLTextAreaElement;
    fireEvent.click(screen.getByRole('button', { name: 'Copy JSON' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Could not copy. Select the JSON and copy it manually.');
    expect(editor).toBeEnabled();
    expect(editor).toHaveAttribute('readonly');
    editor.focus();
    editor.select();
    expect(editor.selectionStart).toBe(0);
    expect(editor.selectionEnd).toBe(editor.value.length);
  });

  it('ignores a pending clipboard result after leaving the JSON view', async () => {
    mockAPI();
    let finishCopy!: () => void;
    const writeText = vi.fn(() => new Promise<void>(resolve => { finishCopy = resolve; }));
    vi.stubGlobal('navigator', Object.assign(Object.create(navigator), { clipboard: { writeText } }));
    const props = { connection, credentials, onSignIn: vi.fn(), selectedID: mission.name, onSelect: vi.fn(), onViewChange: vi.fn() };
    const rendered = render(<MissionsPage {...props} view="json" />);
    fireEvent.click(await screen.findByRole('button', { name: 'Copy JSON' }));
    rendered.rerender(<MissionsPage {...props} view="details" />);
    await screen.findByRole('heading', { name: 'Starting actions' });
    rendered.rerender(<MissionsPage {...props} view="json" />);
    await screen.findByRole('textbox', { name: 'Mission definition (JSON)' });
    await act(async () => finishCopy());
    expect(screen.queryByText('Copied')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy JSON' })).toBeEnabled();
  });

  it('renders an empty catalog and nullable definition fields without inventing data', async () => {
    const empty: Mission = {
      name: 'manual-review', objectives: ['A manual review.'], starting_actions: null, required_actions: null,
      input_schema: null, output_schema: null,
    };
    let items: Mission[] = [];
    mockAPI(url => url.endsWith('/api/v1/missions') ? Response.json({ items }) : Response.json(empty));
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('heading', { name: 'No missions yet' })).toBeVisible();
    expect(screen.queryByRole('searchbox')).not.toBeInTheDocument();

    items = [empty];
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    fireEvent.click(await screen.findByRole('button', { name: 'View mission manual-review' }));

    expect(await screen.findByText('No rules defined.')).toBeVisible();
    expect(screen.getByText('Valid proposals run automatically unless a rule requires model review. Failed work and invalid or empty proposals require model review.')).toBeVisible();
    expect(screen.queryByRole('table', { name: 'Mission rules' })).not.toBeInTheDocument();
    expect(screen.getAllByText('None specified')).toHaveLength(2);
    expect(screen.queryByRole('heading', { name: 'Optional actions' })).not.toBeInTheDocument();
    expect(screen.getAllByText('No schema specified.')).toHaveLength(2);
    expect(screen.queryByText('discover-pages')).not.toBeInTheDocument();
  });

  it('retries a failed catalog request without exposing the server error body', async () => {
    let failed = true;
    mockAPI(() => failed ? Response.json({ error: 'internal database credentials' }, { status: 503 })
      : Response.json({ items: [mission] }));
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load missions. Check the connection and try again.');
    expect(screen.queryByText('internal database credentials')).not.toBeInTheDocument();

    failed = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    expect(await screen.findByRole('button', { name: 'View mission review-website-with-browser' })).toBeVisible();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('returns to the catalog when a mission is deleted before it is opened', async () => {
    let items = [mission];
    mockAPI(url => url.endsWith('/api/v1/missions') ? Response.json({ items }) : new Response(null, { status: 404 }));
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'View mission review-website-with-browser' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('This mission is no longer available.');
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();

    items = [];
    fireEvent.click(screen.getByRole('button', { name: 'All missions' }));

    expect(await screen.findByRole('heading', { name: 'No missions yet' })).toBeVisible();
  });

  it('requests sign-in before loading missions and offers it again for rejected credentials', async () => {
    const fetch = mockAPI(() => new Response(null, { status: 401 }));
    const onSignIn = vi.fn();
    const view = render(<MissionsPage connection={connection} onSignIn={onSignIn} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Sign in to view missions on this connection.');
    expect(fetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(onSignIn).toHaveBeenCalledOnce();

    view.rerender(<MissionsPage connection={connection} credentials={credentials} onSignIn={onSignIn} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('Sign in to view missions on this connection.');
    expect(fetch).toHaveBeenCalledOnce();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeEnabled();
  });

  it('shows permission errors without presenting them as an empty catalog', async () => {
    mockAPI(() => new Response(null, { status: 403 }));
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('This connection does not have permission to view missions.');
    expect(screen.queryByRole('heading', { name: 'No missions yet' })).not.toBeInTheDocument();
  });

  it('cancels a detail request when returning and ignores its late response', async () => {
    const detail = deferredResponse();
    let signal: AbortSignal | undefined;
    mockAPI((url, init) => {
      if (url.endsWith('/api/v1/missions')) return Response.json({ items: [mission] });
      signal = init.signal ?? undefined;
      return detail.promise;
    });
    render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'View mission review-website-with-browser' }));
    expect(screen.getByText('Loading mission…')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'All missions' }));
    expect(signal?.aborted).toBe(true);

    await act(async () => detail.resolve(Response.json(mission)));

    expect(await screen.findByRole('button', { name: 'View mission review-website-with-browser' })).toBeVisible();
    expect(screen.queryByRole('heading', { name: 'Starting actions' })).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('does not show a previous connection’s late catalog after switching connections', async () => {
    const first = deferredResponse();
    let signal: AbortSignal | undefined;
    const nextConnection = { ...connection, id: 'other', url: 'https://other.example.test' };
    mockAPI((url, init) => {
      if (url.startsWith(connection.url)) { signal = init.signal ?? undefined; return first.promise; }
      return Response.json({ items: [otherMission] });
    });
    const view = render(<MissionsPage connection={connection} credentials={credentials} onSignIn={vi.fn()} />);

    view.rerender(<MissionsPage connection={nextConnection} credentials={{ ...credentials, url: nextConnection.url }} onSignIn={vi.fn()} />);
    expect(signal?.aborted).toBe(true);
    expect(await screen.findByRole('button', { name: 'View mission review-service' })).toBeVisible();
    await act(async () => first.resolve(Response.json({ items: [mission] })));

    expect(screen.queryByRole('button', { name: 'View mission review-website-with-browser' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'View mission review-service' })).toBeVisible();
  });

  it('hides loaded data immediately when credentials are forgotten and cancels a pending refresh', async () => {
    const refresh = deferredResponse();
    let signal: AbortSignal | undefined;
    let calls = 0;
    mockAPI((_url, init) => {
      if (++calls === 1) return Response.json({ items: [mission] });
      signal = init.signal ?? undefined;
      return refresh.promise;
    });
    const onSignIn = vi.fn();
    const view = render(<MissionsPage connection={connection} credentials={credentials} onSignIn={onSignIn} />);
    await screen.findByRole('button', { name: 'View mission review-website-with-browser' });

    view.rerender(<MissionsPage connection={connection} onSignIn={onSignIn} />);
    expect(screen.queryByRole('button', { name: 'View mission review-website-with-browser' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeVisible();
    view.rerender(<MissionsPage connection={connection} credentials={{ ...credentials }} onSignIn={onSignIn} />);
    await waitFor(() => expect(signal).toBeDefined());
    view.rerender(<MissionsPage connection={connection} onSignIn={onSignIn} />);
    expect(signal?.aborted).toBe(true);
    await act(async () => refresh.resolve(Response.json({ items: [mission] })));

    expect(screen.queryByRole('button', { name: 'View mission review-website-with-browser' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeVisible();
    expect(calls).toBe(2);
  });

  it('builds a mission from objectives and registered action roles without requiring readiness', async () => {
    const onSelect = vi.fn();
    const fetch = mockAPI((_url, init) => init.method === 'POST'
      ? Response.json(JSON.parse(init.body as string), { status: 201 }) : Response.json({ items: [] }));
    render(<CreateMissionPage connection={connection} credentials={credentials} onSignIn={vi.fn()} onBack={vi.fn()} onSelect={onSelect} />);
    expect(screen.getByRole('textbox', { name: 'Mission name' })).toHaveFocus();
    expect(screen.getByRole('button', { name: 'Remove objective 1' })).toBeDisabled();
    fireEvent.change(screen.getByRole('textbox', { name: 'Mission name' }), { target: { value: 'guided-review' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Objective 1' }), { target: { value: 'Find public pages.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add objective' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Objective 2' }), { target: { value: 'Capture release evidence.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add objective' }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove objective 3' }));

    const discover = await screen.findByRole('combobox', { name: 'Role for discover-pages' });
    const starting = screen.getByRole('checkbox', { name: 'Start with discover-pages' });
    expect(starting).toBeDisabled();
    fireEvent.change(discover, { target: { value: 'required' } });
    fireEvent.click(starting);
    expect(starting).toBeChecked();
    fireEvent.change(discover, { target: { value: 'optional' } });
    expect(starting).not.toBeChecked();
    expect(starting).toBeDisabled();
    fireEvent.change(discover, { target: { value: 'required' } });
    fireEvent.click(starting);
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search actions' }), { target: { value: 'screenshot' } });
    expect(screen.queryByRole('combobox', { name: 'Role for discover-pages' })).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('combobox', { name: 'Role for capture-page' }), { target: { value: 'optional' } });
    expect(screen.getByRole('checkbox', { name: 'Start with capture-page' })).toBeDisabled();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search actions' }), { target: { value: '' } });
    expect(screen.getByRole('checkbox', { name: 'Start with discover-pages' })).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));

    await waitFor(() => expect(onSelect).toHaveBeenCalledExactlyOnceWith('guided-review'));
    const posts = fetch.mock.calls.filter(([, init]) => init?.method === 'POST');
    expect(posts).toHaveLength(1);
    expect(JSON.parse(posts[0][1]!.body as string)).toMatchObject({
      name: 'guided-review', objectives: ['Find public pages.', 'Capture release evidence.'],
      required_actions: ['discover-pages'], optional_actions: ['capture-page'], starting_actions: ['discover-pages'],
    });
  });

  it('preserves advanced JSON through form edits and rejects invalid mode transitions without losing text', async () => {
    const steps = [{ id: 'check', objective: 'Review the page.', agent_types: ['web-agent'], required_actions: ['check-links'] }];
    const definitions = [{ name: 'capture-page', description: 'Capture requested evidence.',
      input_schema: { type: 'object' }, output_schema: { type: 'object' }, required_secrets: ['SITE_TOKEN'] }];
    const source = JSON.stringify({ ...mission, steps, action_definitions: definitions })
      .replace('"default":3', String.raw`"const":9007199254740993,"description":"\u0061\\path"`);
    const fetch = mockAPI();
    render(<CreateMissionPage connection={connection} credentials={credentials} onSignIn={vi.fn()} onBack={vi.fn()} onSelect={vi.fn()} />);
    fireEvent.change(creationJSONEditor(), { target: { value: source } });
    fireEvent.click(screen.getByRole('button', { name: 'Form' }));
    expect(screen.getByRole('textbox', { name: 'Mission name' })).toHaveValue(mission.name);
    fireEvent.change(screen.getByRole('textbox', { name: 'Mission name' }), { target: { value: 'edited-review' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Objective 2' }), { target: { value: 'Keep ordered evidence.' } });
    fireEvent.click(screen.getByText('Advanced settings', { selector: 'summary' }));
    const schema = screen.getByRole('textbox', { name: 'Input schema (JSON)' });
    const schemaText = (schema as HTMLTextAreaElement).value;
    expect(schemaText).toContain('9007199254740993');
    expect(schemaText).toContain(String.raw`"\u0061\\path"`);
    fireEvent.change(schema, { target: { value: '{invalid' } });
    fireEvent.click(screen.getByRole('button', { name: 'JSON' }));
    expect(screen.getByRole('alert')).toHaveTextContent('Enter valid JSON with the expected field types in the advanced settings.');
    expect(schema).toHaveValue('{invalid');
    expect(screen.getByRole('button', { name: 'Form' })).toHaveAttribute('aria-pressed', 'true');
    fireEvent.change(schema, { target: { value: schemaText } });
    const editor = creationJSONEditor();
    const edited = (editor as HTMLTextAreaElement).value;
    expect(edited).toContain('9007199254740993');
    expect(edited).toContain(String.raw`"\u0061\\path"`);
    expect(JSON.parse(edited)).toMatchObject({ name: 'edited-review', objectives: [mission.objectives[0], 'Keep ordered evidence.'],
      rules: mission.rules, steps, action_definitions: definitions });
    fireEvent.change(editor, { target: { value: '{invalid' } });
    fireEvent.click(screen.getByRole('button', { name: 'Form' }));
    expect(screen.getByRole('alert')).toHaveTextContent('This definition cannot be shown in the form. Check its field types or continue editing JSON.');
    expect(editor).toHaveValue('{invalid');
    expect(screen.getByRole('button', { name: 'JSON' })).toHaveAttribute('aria-pressed', 'true');
    expect(fetch.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
  });

  it('still saves JSON when the action catalog cannot be loaded', async () => {
    const onSelect = vi.fn();
    const fetch = vi.fn(async (_input: RequestInfo | URL, init: RequestInit = {}) => init.method === 'POST'
      ? Response.json(mission, { status: 201 }) : new Response(null, { status: 503 }));
    vi.stubGlobal('fetch', fetch);
    render(<CreateMissionPage connection={connection} credentials={credentials} onSignIn={vi.fn()} onBack={vi.fn()} onSelect={onSelect} />);
    await screen.findByRole('alert');
    fireEvent.change(creationJSONEditor(), { target: { value: JSON.stringify(mission) } });
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
    await waitFor(() => expect(onSelect).toHaveBeenCalledExactlyOnceWith(mission.name));
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });

  it('focuses the definition, preserves rejected drafts and creates only after an explicit corrected submission', async () => {
    const created = { ...mission, name: 'custom-review' };
    const rejectedDraft = JSON.stringify({ ...created, optional_actions: 'capture-page' });
    const definition = JSON.stringify(created);
    let submissions = 0;
    const fetch = mockAPI((url, init) => {
      if (init.method === 'POST') return ++submissions === 1
        ? Response.json({ error: 'private-validation-details' }, { status: 400 })
        : Response.json(created, { status: 201 });
      return url.endsWith('/api/v1/missions') ? Response.json({ items: [mission] }) : Response.json(created);
    });
    const onSelect = vi.fn();
    render(<CreateMissionPage connection={connection} credentials={credentials} onSignIn={vi.fn()} onBack={vi.fn()} onSelect={onSelect} />);
    const editor = creationJSONEditor();
    expect(editor).toHaveFocus();
    expect(JSON.parse((editor as HTMLTextAreaElement).value).objectives).toEqual(['']);
    fireEvent.change(editor, { target: { value: '{invalid json' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter valid JSON.');
    expect(submissions).toBe(0);

    fireEvent.change(editor, { target: { value: rejectedDraft } });
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Check the mission definition');
    expect(editor).toHaveValue(rejectedDraft);
    expect(editor).toBeEnabled();
    expect(editor).toHaveFocus();
    expect(submissions).toBe(1);
    expect(screen.queryByText('private-validation-details')).not.toBeInTheDocument();

    fireEvent.change(editor, { target: { value: definition } });
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
    await waitFor(() => expect(onSelect).toHaveBeenCalledExactlyOnceWith(created.name));
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'POST').map(([, init]) => init?.body))
      .toEqual([rejectedDraft, definition]);
  });

  it('locks an uncertain save and offers its saved definition without repeating the write', async () => {
    const created = { ...mission, name: 'uncertain-review' };
    const fetch = mockAPI((url, init) => {
      if (init.method === 'POST') throw new TypeError('private-network-details');
      return url.endsWith('/api/v1/missions') ? Response.json({ items: [mission] }) : Response.json(created);
    });
    const onSelect = vi.fn();
    render(<CreateMissionPage connection={connection} credentials={credentials} onSignIn={vi.fn()} onBack={vi.fn()} onSelect={onSelect} />);
    const editor = creationJSONEditor();
    fireEvent.change(editor, { target: { value: JSON.stringify(created) } });
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('The save result is unknown. Check the saved mission before trying again.');
    expect(editor).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Create mission' })).toBeDisabled();
    expect(screen.queryByText('private-network-details')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Check saved mission' }));

    expect(onSelect).toHaveBeenCalledExactlyOnceWith(created.name);
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });

  it('discards the draft and aborts pending creation when its session or connection changes', async () => {
    const pending: ReturnType<typeof deferredResponse>[] = [];
    const signals: AbortSignal[] = [];
    mockAPI((_url, init) => {
      if (init.method !== 'POST') return Response.json({ items: [mission] });
      const response = deferredResponse();
      pending.push(response);
      signals.push(init.signal!);
      return response.promise;
    });
    const callbacks = { onSignIn: vi.fn(), onBack: vi.fn(), onSelect: vi.fn() };
    const view = render(<CreateMissionPage connection={connection} credentials={credentials} {...callbacks} />);
    const nextConnection = { ...connection, id: 'other', url: 'https://other.example.test' };
    const sessions = [
      { connection, credentials: { ...credentials, username: 'another-operator' } },
      { connection: nextConnection, credentials: { ...credentials, url: nextConnection.url } },
    ];
    for (const [index, next] of sessions.entries()) {
      const editor = creationJSONEditor();
      expect(JSON.parse((editor as HTMLTextAreaElement).value).name).toBe('');
      fireEvent.change(editor, { target: { value: JSON.stringify({ ...mission, name: 'abandoned-review' }) } });
      fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
      expect(await screen.findByRole('status')).toHaveTextContent('Saving mission…');

      view.rerender(<CreateMissionPage {...next} {...callbacks} />);
      expect(signals[index].aborted).toBe(true);
      expect(screen.getByRole('textbox', { name: 'Mission name' })).toHaveValue('');
      const resetEditor = creationJSONEditor();
      expect(JSON.parse((resetEditor as HTMLTextAreaElement).value).name).toBe('');
      expect(resetEditor).toBeEnabled();
      await act(async () => pending[index].resolve(Response.json({ ...mission, name: 'abandoned-review' }, { status: 201 })));

      expect(screen.getByRole('heading', { name: 'Create mission', level: 1 })).toBeVisible();
      expect(callbacks.onSelect).not.toHaveBeenCalled();
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    }
    expect(pending).toHaveLength(2);
  });

  it('aborts a pending save after cancellation and ignores its late result', async () => {
    const response = deferredResponse();
    let signal: AbortSignal | undefined;
    const fetch = mockAPI((_url, init) => {
      signal = init.signal ?? undefined;
      return response.promise;
    });
    const onBack = vi.fn();
    const onSelect = vi.fn();
    const view = render(<CreateMissionPage connection={connection} credentials={credentials} onSignIn={vi.fn()} onBack={onBack} onSelect={onSelect} />);
    fireEvent.change(creationJSONEditor(), { target: { value: JSON.stringify(mission) } });
    fireEvent.click(screen.getByRole('button', { name: 'Create mission' }));
    await screen.findByText('Saving mission…');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onBack).toHaveBeenCalledOnce();
    view.unmount();
    expect(signal?.aborted).toBe(true);
    await act(async () => response.resolve(Response.json(mission, { status: 201 })));
    expect(onSelect).not.toHaveBeenCalled();
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });
});

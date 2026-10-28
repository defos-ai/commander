import { act, createEvent, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SecretsPage } from '../src/pages/SecretsPage';
import { upstreamURL } from './fakes/proxy';
import { deferredResponse } from './fixtures/deferred';
import { connection, credentials, fixture, pending, secret, secretPath, secretsPath, secretValue, view } from './fixtures/secrets';

async function provide() {
  fireEvent.click(await screen.findByRole('button', { name: 'Provide value' }));
  const dialog = await screen.findByRole('dialog', { name: 'Provide value' });
  await waitFor(() => expect(within(dialog).getByLabelText('Value')).toBeEnabled());
  return dialog;
}

async function showSaved() {
  fireEvent.change(await screen.findByLabelText('Agent type'), { target: { value: secret.agent_type } });
  const show = screen.getByRole('button', { name: 'Show' });
  await waitFor(() => expect(show).toBeEnabled());
  fireEvent.click(show);
  return screen.findByRole('button', { name: `Delete ${secret.name}` });
}

function fillValue(dialog: HTMLElement) {
  const input = within(dialog).getByLabelText('Value');
  expect(input).toHaveAttribute('type', 'password');
  fireEvent.change(input, { target: { value: secretValue } });
  return input;
}

describe('secret management', () => {
  it('requires a signed-in connection before requesting secret metadata', async () => {
    const api = fixture();
    const onSignIn = vi.fn();
    render(<SecretsPage connection={connection} onSignIn={onSignIn} />);

    expect(screen.getByRole('alert')).toHaveTextContent('Sign in to manage secrets');
    expect(api.fetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(onSignIn).toHaveBeenCalledOnce();
  });

  it('supplies a pending secret through the authenticated proxy and refreshes delivery metadata without exposing its value', async () => {
    const saved = deferredResponse();
    const api = fixture((path, init) => path === secretsPath && init.method === 'POST' ? saved.promise : undefined);
    const storage = vi.spyOn(Storage.prototype, 'setItem');
    view();
    const dialog = await provide();
    const input = fillValue(dialog);
    expect(dialog).toHaveTextContent(secret.agent_type);
    expect(dialog).toHaveTextContent(secret.name);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save secret' }));
    fireEvent.submit(dialog.querySelector('form')!);

    expect(api.writes()).toHaveLength(1);
    expect(input).toHaveValue('');
    const [url, options] = api.writes()[0];
    expect(String(url)).toMatch(/^\/ui\/proxy\?/);
    expect(upstreamURL(url)).toBe(`${connection.url}${secretsPath}`);
    expect(options).toMatchObject({ method: 'POST', credentials: 'same-origin', cache: 'no-store', redirect: 'error' });
    expect(new Headers(options.headers).get('Authorization')).toBeNull();
    expect(JSON.parse(options.body as string)).toEqual({ agent_type: secret.agent_type, name: secret.name, value: secretValue });
    expect(storage).not.toHaveBeenCalled();
    expect(document.body).not.toHaveTextContent(secretValue);

    api.change(state => { state.current = { ...secret, version: 1 }; });
    api.change(state => { state.secrets = [state.current!]; });
    api.change(state => { state.requests = [{ ...pending, status: 'awaiting_installation' }]; });
    await act(async () => saved.resolve(Response.json({ ...secret, version: 1 }, { status: 201 })));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(await screen.findByText('Awaiting installation')).toBeVisible();
    expect(api.writes()).toHaveLength(1);
    expect(document.body).not.toHaveTextContent(secretValue);
    expect(storage).not.toHaveBeenCalled();
  });

  it.each(['\n', '\r\n'])('rejects multiline paste into the masked input without silently removing line breaks (%j)', async separator => {
    const api = fixture();
    view();
    const dialog = await provide();
    const input = within(dialog).getByLabelText('Value');
    const paste = createEvent.paste(input, {
      cancelable: true,
      clipboardData: { getData: () => `first-line${separator}second-line` },
    });
    fireEvent(input, paste);

    expect(paste.defaultPrevented).toBe(true);
    expect(input).toHaveAttribute('type', 'password');
    expect(input).toHaveValue('');
    expect(within(dialog).getByRole('alert')).toHaveTextContent('Use multiline input to preserve them');
    expect(within(dialog).queryByRole('textbox', { name: 'Value' })).not.toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Save secret' })).toBeDisabled();
    expect(api.writes()).toHaveLength(0);
  });

  it('requires explicit visible input for multiline secrets and submits the entered text unchanged', async () => {
    const saved = deferredResponse();
    const api = fixture((path, init) => path === secretsPath && init.method === 'POST' ? saved.promise : undefined);
    const multiline = ' \n-----BEGIN PRIVATE KEY-----\n  test-only-key-material  \n-----END PRIVATE KEY-----\n';
    view();
    const dialog = await provide();
    expect(within(dialog).getByLabelText('Value')).toHaveAttribute('type', 'password');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Use multiline input (visible)' }));
    const input = within(dialog).getByRole('textbox', { name: 'Value' });
    expect(input.tagName).toBe('TEXTAREA');
    fireEvent.change(input, { target: { value: multiline } });
    expect(input).toHaveValue(multiline);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save secret' }));

    expect(api.writes()).toHaveLength(1);
    expect(JSON.parse(api.writes()[0][1].body as string).value).toBe(multiline);
    expect(input).toHaveValue('');
    expect(dialog).not.toHaveTextContent('test-only-key-material');
    await act(async () => saved.resolve(Response.json({ ...secret, version: 1 }, { status: 201 })));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('replaces an expired value for a pending request using the current identity and version', async () => {
    const api = fixture((path, init) => {
      if (path !== secretPath || init.method !== 'PUT') return undefined;
      api.change(state => { state.current = { ...secret, version: secret.version + 1 }; });
      api.change(state => { state.secrets = [state.current!]; });
      return Response.json(api.snapshot().current);
    });
    api.change(state => { state.current = { ...secret, status: 'expired', expires_at: '2020-01-01T00:00:00Z' }; });
    api.change(state => { state.secrets = [state.current!]; });
    view();
    const dialog = await provide();
    fillValue(dialog);
    fireEvent.click(within(dialog).getByRole('button', { name: /Save secret|Replace secret/ }));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(api.writes()).toHaveLength(1);
    const [url, init] = api.writes()[0];
    expect(upstreamURL(url)).toBe(`${connection.url}${secretPath}`);
    expect(init.method).toBe('PUT');
    expect(JSON.parse(init.body as string)).toMatchObject({ id: secret.id, version: secret.version, value: secretValue });
    expect(JSON.parse(init.body as string)).not.toHaveProperty('expires_at');
  });

  it('adds a secret for an agent type that has no registered agent', async () => {
    const api = fixture((path, init) => {
      if (path !== secretsPath || init.method !== 'POST') return undefined;
      const body = JSON.parse(init.body as string);
      api.change(state => { state.secrets = [{ ...secret, version: 1, agent_type: body.agent_type, name: body.name }]; });
      return Response.json(api.snapshot().secrets[0], { status: 201 });
    });
    api.change(state => { state.requests = []; });
    view();
    const add = await screen.findByRole('button', { name: 'Add secret' });
    await waitFor(() => expect(add).toBeEnabled());
    fireEvent.click(add);
    const dialog = screen.getByRole('dialog', { name: 'Add secret' });
    fireEvent.change(within(dialog).getByLabelText('Agent type'), { target: { value: 'deployer-agent' } });
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'DEPLOY_TOKEN' } });
    fillValue(dialog);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save secret' }));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(api.writes()).toHaveLength(1);
    expect(JSON.parse(api.writes()[0][1].body as string)).toEqual({ agent_type: 'deployer-agent', name: 'DEPLOY_TOKEN', value: secretValue });
  });

  it('requires confirmation before deleting and sends the displayed record identity and version', async () => {
    const api = fixture((path, init) => {
      if (!path.startsWith(`${secretPath}?`) || init.method !== 'DELETE') return undefined;
      api.change(state => { state.secrets = []; });
      return new Response(null, { status: 204 });
    });
    api.change(state => { state.requests = []; });
    api.change(state => { state.secrets = [secret]; });
    view();
    fireEvent.click(await showSaved());
    let dialog = screen.getByRole('dialog', { name: 'Delete secret' });
    expect(dialog).toHaveTextContent(secret.name);
    expect(api.writes()).toHaveLength(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(api.writes()).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: `Delete ${secret.name}` }));
    dialog = screen.getByRole('dialog', { name: 'Delete secret' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete secret' }));

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(api.writes()).toHaveLength(1);
    const [url, init] = api.writes()[0];
    const target = new URL(upstreamURL(url));
    expect(target.pathname).toBe(secretPath);
    expect(target.searchParams.get('id')).toBe(secret.id);
    expect(target.searchParams.get('version')).toBe(String(secret.version));
    expect(init.body).toBeUndefined();
    expect(screen.queryByRole('button', { name: `Delete ${secret.name}` })).not.toBeInTheDocument();
  });

  it('keeps secret writes disabled when the API has no encryption key configured', async () => {
    const api = fixture(path => path.startsWith(secretsPath)
      ? Response.json({ error: 'secrets_unavailable', detail: 'private server configuration' }, { status: 503 }) : undefined);
    view();

    expect(await screen.findByRole('alert')).toHaveTextContent(/not configured|unavailable|not enabled/i);
    expect(screen.getByRole('button', { name: 'Add secret' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Provide value' })).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent('private server configuration');
    expect(api.writes()).toHaveLength(0);
  });

  it.each([
    { code: 'secret_conflict', status: 409 },
    { code: 'secret_write_uncertain', status: 503 },
  ])('clears the submitted value and prevents resubmission after $code', async ({ code, status }) => {
    const api = fixture((path, init) => path === secretsPath && init.method === 'POST'
      ? Response.json({ error: code, detail: secretValue }, { status }) : undefined);
    view();
    const dialog = await provide();
    const input = fillValue(dialog);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save secret' }));

    expect(await within(dialog).findByRole('alert')).toBeVisible();
    expect(input).toHaveValue('');
    expect(within(dialog).queryByRole('button', { name: 'Save secret' })).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent(secretValue);
    fireEvent.submit(dialog.querySelector('form')!);
    expect(api.writes()).toHaveLength(1);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close and refresh' }));
    await screen.findByRole('button', { name: 'Provide value' });
    expect(api.writes()).toHaveLength(1);
  });

  it('does not treat a failed metadata lookup as permission to create a value', async () => {
    const api = fixture((path, init) => path === secretPath && init.method === 'GET'
      ? Response.json({ error: 'internal_error', detail: secretValue }, { status: 503 }) : undefined);
    view();
    fireEvent.click(await screen.findByRole('button', { name: 'Provide value' }));
    const dialog = screen.getByRole('dialog', { name: 'Provide value' });

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Could not reach the secrets service');
    expect(within(dialog).getByLabelText('Value')).toBeDisabled();
    expect(within(dialog).queryByRole('button', { name: 'Save secret' })).not.toBeInTheDocument();
    fireEvent.submit(dialog.querySelector('form')!);
    expect(api.writes()).toHaveLength(0);
    expect(document.body).not.toHaveTextContent(secretValue);
  });

  it('discards an unsubmitted value when a dialog closes', async () => {
    const api = fixture();
    view();
    let dialog = await provide();
    fillValue(dialog);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    dialog = await provide();

    expect(within(dialog).getByLabelText('Value')).toHaveValue('');
    expect(api.writes()).toHaveLength(0);
  });

  it('aborts a submitted write and ignores its late success when switching connections', async () => {
    const saved = deferredResponse();
    const nextConnection = { ...connection, id: 'other', url: 'https://other.example.test' };
    const api = fixture((_path, init, url) => {
      if (init.method === 'POST') return saved.promise;
      if (url.startsWith(nextConnection.url)) return Response.json({ items: [] });
      return undefined;
    });
    const rendered = view();
    const dialog = await provide();
    fillValue(dialog);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save secret' }));
    const signal = api.writes()[0][1].signal;
    expect(signal?.aborted).toBe(false);

    rendered.rerender(<SecretsPage connection={nextConnection} credentials={{ ...credentials, url: nextConnection.url }} onSignIn={vi.fn()} />);
    expect(signal?.aborted).toBe(true);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByText(secret.name)).not.toBeInTheDocument();
    await act(async () => saved.resolve(Response.json({ ...secret, version: 1 }, { status: 201 })));

    expect(screen.queryByText(secret.name)).not.toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent(secretValue);
    expect(api.writes()).toHaveLength(1);
  });
});

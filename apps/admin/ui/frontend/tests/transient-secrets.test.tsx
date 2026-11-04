import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SecretsPage } from '../src/pages/SecretsPage';
import { deferredResponse } from './fixtures/deferred';
import { connection, credentials, reusableRequest, transientRequest, view } from './fixtures/transient-secrets';
import { transientSecretsAPI } from './fakes/transient-secrets';

const privateValue = 'fixture-private-one-time-code';

function transientSection() {
  return screen.getByRole('heading', { name: 'One-time requests' }).closest('section')!;
}

async function openFirst() {
  await act(async () => { fireEvent.click(within(transientSection()).getAllByRole('button', { name: 'Provide value' })[0]); });
  return screen.getByRole('dialog', { name: 'Provide one-time value' });
}

describe('transient secret requests', () => {
  it('keeps same-name requests separate and receives an earlier receipt without aborting its submission', async () => {
    vi.useFakeTimers();
    try {
      const request = transientRequest();
      const other = { ...transientRequest('2'), agent_id: 'browser-agent-00000002' };
      const response = deferredResponse();
      const api = transientSecretsAPI([request, other], () => response.promise, undefined, [reusableRequest()]);
      await act(async () => { view(); });

      expect(within(transientSection()).getAllByText('CODE', { exact: true })).toHaveLength(2);
      expect(within(transientSection()).getAllByRole('button', { name: 'Provide value' })).toHaveLength(2);
      const dialog = await openFirst();
      const input = within(dialog).getByLabelText('Value', { exact: true });
      expect(input).toHaveAttribute('type', 'password');
      expect(input).toHaveValue('');
      fireEvent.change(input, { target: { value: privateValue } });

      await act(async () => { fireEvent.click(within(dialog).getByRole('button', { name: 'Send value' })); });

      expect(api.posts()).toHaveLength(1);
      expect(new URL(String(api.posts()[0][0]), 'http://ui.test').searchParams.get('path')).toBe(`/api/v1/secret-requests/${request.id}/values`);
      expect(api.posts()[0][1]!.body).toBe(JSON.stringify({ value: privateValue }));
      const submittedInput = within(dialog).queryByLabelText('Value', { exact: true });
      if (submittedInput) expect(submittedInput).toHaveValue('');
      api.change(request.id, { status: 'fulfilled' });

      await act(async () => { await vi.advanceTimersByTimeAsync(2_100); });

      expect(within(dialog).getByText(/Received by agent/)).toBeVisible();
      expect(api.posts()[0][1]!.signal?.aborted).toBe(false);
      await act(async () => { response.resolve(Response.json({ status: 'awaiting_receipt' }, { status: 202 })); });
      expect(within(dialog).getByText(/Received by agent/)).toBeVisible();
      expect(api.posts()).toHaveLength(1);
      expect(document.body.textContent).not.toContain(privateValue);
      expect(JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } })).not.toContain(privateValue);
      for (const [url] of api.fetch.mock.calls) expect(String(url)).not.toContain(privateValue);
    } finally { vi.useRealTimers(); }
  });

  it('never repeats an uncertain submission after status checks, dismissal or reopening', async () => {
    const request = transientRequest();
    const api = transientSecretsAPI([request], () => Response.json({ error: 'secret_delivery_uncertain' }, { status: 503 }));
    view();
    await screen.findByRole('heading', { name: 'One-time requests' });
    await screen.findByRole('button', { name: 'Provide value' });
    const dialog = await openFirst();
    fireEvent.change(within(dialog).getByLabelText('Value', { exact: true }), { target: { value: privateValue } });

    fireEvent.click(within(dialog).getByRole('button', { name: 'Send value' }));

    await within(dialog).findByRole('button', { name: 'Check status' });
    await act(async () => { fireEvent.click(within(dialog).getByRole('button', { name: 'Check status' })); });
    expect(within(dialog).queryByRole('button', { name: 'Send value' })).not.toBeInTheDocument();
    expect(api.posts()).toHaveLength(1);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    await act(async () => {});
    fireEvent.click(await screen.findByRole('button', { name: 'View status' }));
    const reopened = screen.getByRole('dialog', { name: 'Provide one-time value' });
    await act(async () => {});
    expect(within(reopened).queryByLabelText('Value', { exact: true })).not.toBeInTheDocument();
    expect(within(reopened).queryByRole('button', { name: 'Send value' })).not.toBeInTheDocument();
    expect(api.posts()).toHaveLength(1);
  });

  it('clears an unsubmitted value when its request expires without sending it', async () => {
    vi.useFakeTimers();
    try {
      const request = { ...transientRequest(), expires_at: new Date(Date.now() + 1_500).toISOString() };
      const api = transientSecretsAPI([request]);
      await act(async () => { view(); });
      const dialog = await openFirst();
      fireEvent.change(within(dialog).getByLabelText('Value', { exact: true }), { target: { value: privateValue } });

      await act(async () => { await vi.advanceTimersByTimeAsync(2_100); });

      expect(within(dialog).getByText(/^Expired\b/)).toBeVisible();
      const input = within(dialog).queryByLabelText('Value', { exact: true });
      if (input) expect(input).toHaveValue('');
      expect(within(dialog).queryByRole('button', { name: 'Send value' })).not.toBeInTheDocument();
      expect(api.posts()).toHaveLength(0);
      expect(document.body.textContent).not.toContain(privateValue);
    } finally { vi.useRealTimers(); }
  });

  it('discards a dialog and ignores a late metadata read when the connection changes', async () => {
    const request = transientRequest();
    const response = deferredResponse();
    const api = transientSecretsAPI([request], undefined, () => response.promise);
    const rendered = view();
    await screen.findByRole('button', { name: 'Provide value' });
    const dialog = await openFirst();
    const submit = within(dialog).queryByRole('button', { name: 'Send value' });
    if (submit) expect(submit).toBeDisabled();

    const next = { ...connection, id: 'another', url: 'https://another.example.test' };
    await act(async () => { rendered.rerender(<SecretsPage connection={next} credentials={{ ...credentials, url: next.url }} onSignIn={vi.fn()} />); });
    await act(async () => { response.resolve(Response.json(request)); });

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(api.posts()).toHaveLength(0);
    const reads = api.fetch.mock.calls.filter(([url]) => String(url).includes(encodeURIComponent(`/api/v1/secret-requests/${request.id}`)));
    expect(reads.some(([, init]) => init?.signal?.aborted)).toBe(true);
  });

  it('clears an unsubmitted draft when the signed-in session changes', async () => {
    const api = transientSecretsAPI([transientRequest()]);
    const rendered = view();
    await screen.findByRole('button', { name: 'Provide value' });
    const dialog = await openFirst();
    fireEvent.change(within(dialog).getByLabelText('Value', { exact: true }), { target: { value: privateValue } });

    await act(async () => {
      rendered.rerender(<SecretsPage connection={connection} credentials={{ ...credentials }} onSignIn={vi.fn()} />);
    });

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    const reopened = await openFirst();
    expect(within(reopened).getByLabelText('Value', { exact: true })).toHaveValue('');
    expect(api.posts()).toHaveLength(0);
    expect(document.body.textContent).not.toContain(privateValue);
  });
});

import { expect } from '@playwright/test';
import type { Agent } from '../../src/api/types';
import { connect, publicAPIURL, test } from '../fixtures/e2e';
import { stoppingAgentSnapshots } from '../fixtures/e2e/agents';
import { AgentStoppingResponses } from '../fakes/agent-stopping';

test('stop an agent by exact ID and distinguish accepted shutdown from confirmed exit', async ({ page, api }) => {
  const agents = (await api.get<{ items: Agent[] }>('/agents')).items.filter(agent => agent.type === 'example-agent');
  expect(agents).toHaveLength(2);
  const snapshots = stoppingAgentSnapshots(agents[0], agents[1]);
  const responses = new AgentStoppingResponses([snapshots.ready, snapshots.other]);
  responses.respondOnce(202, { items: [snapshots.requested] }, [snapshots.requested]);
  await page.route(url => url.pathname === '/ui/proxy' && url.searchParams.get('url') === publicAPIURL
    && /^\/api\/v1\/agents(?:\/|$)/.test(url.searchParams.get('path') ?? ''), responses.handle);
  await connect(page);
  await page.getByRole('navigation', { name: 'Workspace navigation' }).getByRole('button', { name: 'Agents', exact: true }).click();
  await page.getByRole('button', { name: `View agent ${snapshots.ready.id}`, exact: true }).click();

  await page.getByRole('button', { name: 'Stop agent', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Stop agent', exact: true });
  await expect(dialog.getByLabel('Agent ID', { exact: true })).toHaveValue(snapshots.ready.id);
  await expect(dialog.getByLabel('Agent ID', { exact: true })).toHaveJSProperty('readOnly', true);
  await expect(dialog.getByRole('button', { name: 'Request stop', exact: true })).toBeDisabled();
  await dialog.getByRole('checkbox', { name: 'I confirm stopping the agents selected above.', exact: true }).check();
  await dialog.getByRole('button', { name: 'Request stop', exact: true }).click();

  await expect(dialog).not.toBeVisible();
  const results = page.getByRole('region', { name: 'Stop results', exact: true });
  await expect(results.getByRole('listitem').getByText(snapshots.ready.id, { exact: true })).toBeVisible();
  await expect(results.getByText('Stop requested', { exact: true })).toBeVisible();
  await expect(results.getByText('Stopped', { exact: true })).toHaveCount(0);
  expect(responses.submissions).toEqual([{ selector: { id: snapshots.ready.id } }]);

  responses.save(snapshots.stopped);
  await results.getByRole('button', { name: 'Read saved state', exact: true }).click();

  await expect(results.getByText('Stopped', { exact: true })).toBeVisible();
  expect(responses.submissions).toHaveLength(1);
  await expect(page.getByRole('button', { name: 'Stop agent', exact: true })).toBeDisabled();
});

test('stop selectors are submitted unchanged and partial failures retain each target', async ({ page, api }) => {
  const agents = (await api.get<{ items: Agent[] }>('/agents')).items.filter(agent => agent.type === 'example-agent');
  expect(agents).toHaveLength(2);
  const snapshots = stoppingAgentSnapshots(agents[0], agents[1]);
  const responses = new AgentStoppingResponses([snapshots.ready, snapshots.other]);
  responses.respondOnce(202, { items: [snapshots.requested], errors: [{ id: snapshots.other.id, error: 'agent_stop_uncertain' }] }, [snapshots.requested]);
  responses.respondOnce(200, { items: [] });
  await page.route(url => url.pathname === '/ui/proxy' && url.searchParams.get('url') === publicAPIURL
    && /^\/api\/v1\/agents(?:\/|$)/.test(url.searchParams.get('path') ?? ''), responses.handle);
  await connect(page);
  await page.getByRole('navigation', { name: 'Workspace navigation' }).getByRole('button', { name: 'Agents', exact: true }).click();

  await page.getByRole('button', { name: 'Stop agents', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Stop agents', exact: true });
  await expect(dialog.getByRole('button', { name: 'Request stop', exact: true })).toBeDisabled();
  await dialog.getByLabel('Agent type', { exact: true }).fill(snapshots.ready.type);
  await dialog.getByLabel('Version', { exact: true }).fill(snapshots.ready.version);
  await dialog.getByRole('checkbox', { name: 'I confirm stopping the agents selected above.', exact: true }).check();
  await dialog.getByRole('button', { name: 'Request stop', exact: true }).click();

  await expect(dialog).not.toBeVisible();
  const results = page.getByRole('region', { name: 'Stop results', exact: true });
  const accepted = results.getByRole('listitem').filter({ has: page.getByText(snapshots.ready.id, { exact: true }) });
  const uncertain = results.getByRole('listitem').filter({ has: page.getByText(snapshots.other.id, { exact: true }) });
  await expect(accepted.getByText('Stop requested', { exact: true })).toBeVisible();
  await expect(uncertain).toContainText(/uncertain|unknown/i);
  expect(responses.submissions).toEqual([{ selector: { type: snapshots.ready.type, version: snapshots.ready.version } }]);

  responses.save(snapshots.stopped);
  await results.getByRole('button', { name: 'Read saved state', exact: true }).click();

  await expect(accepted.getByText('Stopped', { exact: true })).toBeVisible();
  await expect(uncertain.getByText('Ready', { exact: true })).toBeVisible();
  expect(responses.submissions).toHaveLength(1);
  await results.getByRole('button', { name: 'New stop request', exact: true }).click();
  await dialog.getByLabel('Agent ID', { exact: true }).fill('');
  await dialog.getByLabel('Agent type', { exact: true }).fill(snapshots.ready.type);
  await dialog.getByLabel('Version', { exact: true }).fill('no-matching-version');
  await dialog.getByRole('checkbox', { name: 'I confirm stopping the agents selected above.', exact: true }).check();
  await dialog.getByRole('button', { name: 'Request stop', exact: true }).click();

  await expect(dialog).not.toBeVisible();
  await expect(results.getByText(/no agents match/i)).toBeVisible();
  expect(responses.submissions).toEqual([
    { selector: { type: snapshots.ready.type, version: snapshots.ready.version } },
    { selector: { type: snapshots.ready.type, version: 'no-matching-version' } },
  ]);
});

test('dismissed in-flight stop outcomes survive route changes and refresh without resubmitting', async ({ page, api }) => {
  const agents = (await api.get<{ items: Agent[] }>('/agents')).items.filter(agent => agent.type === 'example-agent');
  expect(agents).toHaveLength(2);
  const snapshots = stoppingAgentSnapshots(agents[0], agents[1]);
  const responses = new AgentStoppingResponses([snapshots.ready, snapshots.other]);
  const release = responses.holdResponse(503, { error: 'agent_stop_uncertain' }, [snapshots.requested]);
  await page.route(url => url.pathname === '/ui/proxy' && url.searchParams.get('url') === publicAPIURL
    && /^\/api\/v1\/agents(?:\/|$)/.test(url.searchParams.get('path') ?? ''), responses.handle);
  await connect(page);
  await page.getByRole('navigation', { name: 'Workspace navigation' }).getByRole('button', { name: 'Agents', exact: true }).click();

  await page.getByRole('button', { name: 'Stop agents', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Stop agents', exact: true });
  await dialog.getByLabel('Agent ID', { exact: true }).fill(snapshots.ready.id);
  await dialog.getByRole('checkbox', { name: 'I confirm stopping the agents selected above.', exact: true }).check();
  try {
    await dialog.getByRole('button', { name: 'Request stop', exact: true }).click();
    await expect.poll(() => responses.submissions.length).toBe(1);
    await dialog.getByRole('button', { name: 'Close dialog', exact: true }).click();
    await expect(dialog).not.toBeVisible();

    const results = page.getByRole('region', { name: 'Stop results', exact: true });
    await expect(results.getByRole('alert')).toContainText(/uncertain|unknown/i);
    await expect(results.getByRole('button', { name: 'New stop request', exact: true })).toHaveCount(0);
    await page.getByRole('button', { name: `View agent ${snapshots.other.id}`, exact: true }).click();
    release();

    await expect(page.getByRole('heading', { name: snapshots.other.id, exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Stop agent', exact: true })).toBeDisabled();
    await expect(results.getByRole('alert')).toContainText(/uncertain|unknown/i);
    await page.getByRole('button', { name: 'All agents', exact: true }).click();
    await page.getByRole('button', { name: 'Refresh', exact: true }).click();
    await expect(page.getByRole('button', { name: `View agent ${snapshots.ready.id}`, exact: true })).toContainText('Stop requested');
    await expect(page.getByRole('button', { name: 'Stop agents', exact: true })).toBeDisabled();
    await expect(results.getByRole('button', { name: 'New stop request', exact: true })).toHaveCount(0);
    expect(responses.submissions).toEqual([{ selector: { id: snapshots.ready.id } }]);

    await results.getByRole('button', { name: 'Read saved state', exact: true }).click();

    await expect(results.getByRole('listitem').getByText(snapshots.ready.id, { exact: true })).toBeVisible();
    await expect(results.getByRole('button', { name: 'New stop request', exact: true })).toBeEnabled();
    expect(responses.submissions).toHaveLength(1);
  } finally { release(); }
});

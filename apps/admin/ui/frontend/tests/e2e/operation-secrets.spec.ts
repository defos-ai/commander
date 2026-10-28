import { expect } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import type { OperationPreflight } from '../../src/api/operation-types';
import { connect, publicAPIURL, test } from '../fixtures/e2e';

test('operation secret guidance stays visible in graph and opens the matching connection', async ({ page, api }) => {
  const operation = await api.post<{ id: string }>('/operations', {
    operation_id: randomUUID(), mission_name: 'analyze-text', input: { text: 'Secret guidance example' },
  }, 201);
  const operationPath = `/api/v1/operations/${operation.id}`;
  let blocked = true;
  // Bundled actions need no secrets. Replace only this operation's preflight;
  // sign-in, operation/graph reads and the Secrets page use the real test stack.
  await page.route(url => url.pathname === '/ui/proxy' && url.searchParams.get('url') === publicAPIURL
    && url.searchParams.get('path') === operationPath, async route => {
    if (route.request().method() !== 'GET') { await route.fallback(); return; }
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    const saved = await response.json();
    const preflight: OperationPreflight = { ready: !blocked, version: blocked ? 1 : 2, actions: [{
      action_name: 'build', ...(blocked ? { unavailable_agents: [{
        agent_id: 'builder-agent-1234abcd', agent_type: 'builder-agent',
        reason: 'Action has missing requirements', missing_requirements: [{ kind: 'secret', name: 'SOURCE_TOKEN' }],
      }] } : { ready_agent_ids: ['builder-agent-1234abcd'] }),
    }] };
    await route.fulfill({ response, json: { ...saved, preflight } });
  });

  await connect(page, { name: 'Secret wait API', url: publicAPIURL });
  const connectionID = new URL(page.url()).hash.split('/')[2];
  const operationURL = `/#/connections/${connectionID}/operations/${operation.id}`;
  await connect(page, { name: 'Other API', url: 'http://api-secondary:4274' });
  const secretRequests: { url: URL; method: string }[] = [];
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.pathname === '/ui/proxy' && url.searchParams.get('path')?.startsWith('/api/v1/secrets')) {
      secretRequests.push({ url, method: request.method() });
    }
  });

  await page.goto(operationURL);
  await expect(page.getByText('Secrets needed', { exact: true })).toBeVisible();
  await expect(page.getByText('builder-agent / SOURCE_TOKEN', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Execution graph', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Execution graph', exact: true })).toBeVisible();
  await expect(page.getByText('Secrets needed', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Manage secrets', exact: true }).click();

  await expect(page.getByRole('heading', { name: 'Secrets', exact: true })).toBeVisible();
  expect(new URL(page.url()).hash).toBe(`#/connections/${connectionID}/secrets`);
  await expect(page.getByRole('button', { name: 'Add secret', exact: true })).toBeEnabled();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(secretRequests.length).toBeGreaterThan(0);
  expect(secretRequests.every(request => request.method === 'GET' && request.url.searchParams.get('url') === publicAPIURL)).toBe(true);

  await page.goBack();
  await expect(page.getByRole('button', { name: 'Manage secrets', exact: true })).toBeVisible();
  blocked = false;
  await page.getByRole('button', { name: 'Details', exact: true }).click();
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.getByText('All required actions have a ready agent in this snapshot.', { exact: true })).toBeVisible();
  await expect(page.getByText('Secrets needed', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Manage secrets', exact: true })).toHaveCount(0);
});

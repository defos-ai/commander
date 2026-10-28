import { expect, test } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import type { SecretMetadata } from '../../src/api/secrets';
import { apiURL, connect, password, username } from '../fixtures/e2e';

test('manage write-only secrets through the signed-in Admin connection', async ({ page, playwright }) => {
  const agentType = `ui-test-${randomUUID()}`;
  const name = 'SERVICE_TOKEN';
  const secretPath = `/api/v1/secrets/${agentType}/${name}`;
  const firstValue = `test-value-${randomUUID()}`;
  const replacement = `replacement-${randomUUID()}\nsecond line`;
  const urls: string[] = [];
  page.on('request', request => urls.push(request.url()));
  const api = await playwright.request.newContext({
    baseURL: apiURL,
    extraHTTPHeaders: { Authorization: `Basic ${Buffer.from(`${username}:${password}`).toString('base64')}` },
    timeout: 15_000,
  });
  try {
    await connect(page);
    const navigation = page.getByRole('navigation', { name: 'Workspace navigation' });
    await expect(navigation.getByRole('button', { name: 'Secrets', exact: true })).toHaveCount(0);
    await navigation.getByRole('button', { name: 'Agents', exact: true }).click();
    await page.getByRole('button', { name: 'Manage secrets', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Secrets', exact: true })).toBeVisible();
    await expect(navigation.getByRole('button', { name: 'Agents', exact: true })).toHaveAttribute('aria-current', 'page');
    await page.getByRole('button', { name: 'Add secret', exact: true }).click();
    const create = page.getByRole('dialog', { name: 'Add secret', exact: true });
    await create.getByLabel('Agent type', { exact: true }).fill(agentType);
    await create.getByLabel('Name', { exact: true }).fill(name);
    await expect(create.getByLabel('Value', { exact: true })).toHaveAttribute('type', 'password');
    await create.getByLabel('Value', { exact: true }).fill(firstValue);
    await create.getByRole('button', { name: 'Save secret', exact: true }).click();
    await expect(create).not.toBeVisible();
    await expect(page.getByRole('button', { name: `Replace ${name}`, exact: true })).toBeVisible();
    const createdResponse = await api.get(secretPath);
    expect(createdResponse.status()).toBe(200);
    const created: SecretMetadata = await createdResponse.json();
    expect(created.version).toBe(1);
    expect(created).not.toHaveProperty('value');

    await page.reload();
    await expect(page.getByRole('heading', { name: 'Secrets', exact: true })).toBeVisible();
    await page.getByLabel('Agent type', { exact: true }).fill(agentType);
    await page.getByRole('button', { name: 'Show', exact: true }).click();
    await page.getByRole('button', { name: `Replace ${name}`, exact: true }).click();
    const replace = page.getByRole('dialog', { name: 'Replace secret', exact: true });
    await expect(replace.getByLabel('Value', { exact: true })).toHaveValue('');
    await replace.getByRole('button', { name: 'Use multiline input (visible)', exact: true }).click();
    await replace.getByLabel('Value', { exact: true }).fill(replacement);
    const replacementRequest = page.waitForRequest(request => request.method() === 'PUT'
      && new URL(request.url()).searchParams.get('path') === secretPath);
    await replace.getByRole('button', { name: 'Replace secret', exact: true }).click();
    expect((await replacementRequest).postDataJSON().value).toBe(replacement);
    await expect(replace).not.toBeVisible();
    const replacedResponse = await api.get(secretPath);
    expect(replacedResponse.status()).toBe(200);
    const replaced: SecretMetadata = await replacedResponse.json();
    expect(replaced.id).toBe(created.id);
    expect(replaced.version).toBe(2);
    expect(replaced).not.toHaveProperty('value');

    await page.getByRole('button', { name: `Delete ${name}`, exact: true }).click();
    const remove = page.getByRole('dialog', { name: 'Delete secret', exact: true });
    await remove.getByRole('button', { name: 'Delete secret', exact: true }).click();
    await expect(remove).not.toBeVisible();
    await expect(page.getByRole('button', { name: `Replace ${name}`, exact: true })).toHaveCount(0);
    expect((await api.get(secretPath)).status()).toBe(404);
    const stored = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
    for (const value of [firstValue, replacement]) {
      expect(stored.includes(value)).toBe(false);
      expect(urls.some(url => url.includes(value))).toBe(false);
      expect((await page.locator('body').innerText()).includes(value)).toBe(false);
    }
  } finally {
    try {
      // Remove only this test's unique key; ordinary operation cleanup retains secrets.
      const current = await api.get(secretPath);
      if (current.status() === 200) {
        const metadata: SecretMetadata = await current.json();
        const removed = await api.delete(`${secretPath}?${new URLSearchParams({ id: metadata.id, version: String(metadata.version) })}`);
        expect(removed.status()).toBe(204);
      } else expect(current.status()).toBe(404);
    } finally { await api.dispose(); }
  }
});

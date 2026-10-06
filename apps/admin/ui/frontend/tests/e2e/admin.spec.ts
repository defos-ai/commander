import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect } from '@playwright/test';
import type { OperationQuestion, OperationTask } from '../../src/api/operation-types';
import type { Report } from '../../src/api/reports';
import { test, connect, startOperation, username, password } from './fixtures';

test('connection catalogs survive browser navigation and restore server sign-in across reload', async ({ page, api }) => {
  await api.ready();
  const browserRequests: { url: string; authorization?: string }[] = [];
  page.on('request', request => browserRequests.push({ url: request.url(), authorization: request.headers().authorization }));
  await connect(page);
  const navigation = page.getByRole('navigation', { name: 'Workspace navigation' });
  await navigation.getByRole('button', { name: 'Missions', exact: true }).click();
  await page.getByRole('button', { name: 'View mission review-website' }).click();
  await expect(page.getByRole('heading', { name: 'review-website', exact: true })).toBeVisible();
  const missionURL = page.url();
  await expect(page).toHaveURL(/\/missions\/review-website$/);

  await navigation.getByRole('button', { name: 'Agents', exact: true }).click();
  await expect(page.getByRole('button', { name: /^View agent / })).toHaveCount(2);
  await page.goBack();
  await expect(page).toHaveURL(missionURL);
  await expect(page.getByRole('heading', { name: 'review-website', exact: true })).toBeVisible();
  await page.goForward();
  await expect(page.getByRole('button', { name: /^View agent / })).toHaveCount(2);
  await page.goBack();
  await page.reload();
  await expect(page).toHaveURL(missionURL);
  await expect(page.getByRole('heading', { name: 'review-website', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toHaveCount(0);
  const browserStorage = await page.evaluate(() => ({ local: { ...localStorage }, session: { ...sessionStorage } }));
  expect(JSON.stringify(browserStorage)).not.toContain(password);
  expect(JSON.stringify(browserStorage)).not.toContain(username);
  expect(browserRequests.every(request => !request.authorization)).toBe(true);
  expect(browserRequests.filter(request => new URL(request.url).pathname.startsWith('/api/'))).toEqual([]);

  const secondURL = 'http://api-secondary:4274';
  await connect(page, { name: 'Second API', url: secondURL });
  await navigation.getByRole('button', { name: 'All connections', exact: true }).click();
  await page.getByRole('button', { name: 'Open Compose test', exact: true }).click();
  await navigation.getByRole('button', { name: 'Connection settings', exact: true }).click();
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(page.getByText('Not signed in', { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByText('Not signed in', { exact: true })).toBeVisible();
  await navigation.getByRole('button', { name: 'All connections', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Open Second API', exact: true })).toHaveAccessibleDescription(`${secondURL} Connected`);
  await page.getByRole('button', { name: 'Open Second API', exact: true }).click();
  await expect(page.getByText('Connected', { exact: true })).toBeVisible();
  await navigation.getByRole('button', { name: 'All connections', exact: true }).click();
  await page.getByRole('button', { name: 'Open Compose test', exact: true }).click();
  await navigation.getByRole('button', { name: 'Missions', exact: true }).click();
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  const signIn = page.getByRole('dialog', { name: 'Edit connection' });
  await expect(signIn.getByLabel('Password', { exact: true })).toHaveValue('');
  await signIn.getByLabel('Username', { exact: true }).fill(username);
  await signIn.getByLabel('Password', { exact: true }).fill(password);
  await signIn.getByRole('button', { name: 'Save connection' }).click();
  await page.getByRole('button', { name: 'View mission review-website' }).click();
  await expect(page).toHaveURL(missionURL);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Open navigation' }).click();
  await navigation.getByRole('button', { name: 'Actions', exact: true }).click();
  await expect(page.getByRole('button', { name: /^View action / })).toHaveCount(6);
  await page.getByRole('button', { name: 'View action capture-page', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'capture-page', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  expect(browserRequests.every(request => !request.authorization)).toBe(true);
  expect(browserRequests.filter(request => new URL(request.url).pathname.startsWith('/api/'))).toEqual([]);
});

test('operation questions, automatic work, graph and downloads use the real stack', async ({ page, api, website }) => {
  await connect(page);
  await page.getByRole('navigation', { name: 'Workspace navigation' }).getByRole('button', { name: 'Operations', exact: true }).click();
  const operation = await startOperation(page, website);
  const path = `/operations/${operation.id}`;
  const question = await api.post<OperationQuestion>(`${path}/questions`, {
    text: 'Which release should this review cover?', options: ['Current release', 'Next release'],
  }, 201);
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await page.getByRole('button', { name: 'Answer question', exact: true }).click();
  const answer = page.getByRole('dialog', { name: 'Answer question', exact: true });
  await answer.getByRole('radio', { name: 'Current release' }).check();
  await answer.getByRole('button', { name: 'Send answer' }).click();
  await expect(answer).not.toBeVisible();
  expect(await api.get<OperationQuestion>(`${path}/questions/${question.id}`)).toMatchObject({ status: 'answered', answer: 'Current release' });

  // Task planning and completion belong to Commander; the admin UI monitors and guides its work.
  const root = await api.post<OperationTask>(`${path}/tasks`, { action_name: 'discover-pages', input: { url: website, max_pages: 1 } }, 201);
  const finished = await api.operation(operation.id, value => value.tasks?.length === 6 && value.tasks.every(task => !['queued', 'running'].includes(task.status))
    && value.tasks.some(task => task.id === root.id && task.continuation?.status === 'pending'));
  expect(finished.tasks?.map(task => task.status)).toEqual(Array(6).fill('succeeded'));
  const artifact = finished.tasks?.flatMap(task => task.result?.artifacts ?? []).find(item => item.action_name === 'capture-page');
  expect(artifact).toBeDefined();
  const file = artifact!.files!.find(item => item.name === 'desktop.png')!;

  await page.getByRole('button', { name: 'Execution graph', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/operations/${operation.id}/graph$`));
  const graph = page.getByRole('region', { name: 'Operation execution graph' });
  await expect(graph.getByRole('button', { name: /^Inspect Task / })).toHaveCount(6);
  await expect(graph.getByRole('button', { name: /^Inspect Artifact / })).toHaveCount(1);
  await expect(graph.getByRole('button', { name: /^Inspect File / })).toHaveCount(2);
  await graph.getByRole('button', { name: /^Inspect Task capture-page / }).click();
  const node = page.getByRole('region', { name: 'Node details' });
  await expect(node.getByRole('button', { name: 'Parent task discover-pages', exact: true })).toBeVisible();
  await expect(node.getByRole('button', { name: 'Produced artifact Page screenshots', exact: true })).toBeVisible();
  await page.getByRole('dialog', { name: 'Node details', exact: true }).getByRole('button', { name: 'Close dialog' }).click();
  await graph.getByRole('button', { name: new RegExp(`^Inspect File desktop\\.png · file:${artifact!.id}:${file.id}$`) }).click();
  await expect(node.getByText(file.sha256, { exact: true })).toBeVisible();
  await node.getByRole('button', { name: 'View file', exact: true }).click();
  const artifactDialog = page.getByRole('dialog', { name: 'Artifact', exact: true });
  const preview = artifactDialog.getByRole('img', { name: 'desktop.png' });
  await expect(preview).toBeVisible();
  await expect.poll(() => preview.evaluate(image => (image as HTMLImageElement).naturalWidth)).toBeGreaterThan(0);
  const screenshotDownload = page.waitForEvent('download');
  await artifactDialog.getByRole('button', { name: 'Download desktop.png', exact: true }).click();
  const screenshot = await screenshotDownload;
  expect(screenshot.suggestedFilename()).toBe('desktop.png');
  const bytes = await readFile((await screenshot.path())!);
  expect(bytes.length).toBe(file.size);
  expect(createHash('sha256').update(bytes).digest('hex')).toBe(file.sha256);
  await artifactDialog.getByRole('button', { name: 'Close', exact: true }).click();

  const decision = finished.tasks!.find(task => task.id === root.id)!.continuation!;
  await api.post(`${path}/tasks/${root.id}/continuation/resolve`, { version: decision.version, actions: [] });
  await api.post(`${path}/complete`, { output: { url: website, summary: 'Reviewed the current release with browser evidence.', findings: [] } });
  await page.getByRole('button', { name: 'Details', exact: true }).click();
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await page.getByRole('button', { name: 'View report', exact: true }).click();
  const reportDialog = page.getByRole('dialog', { name: 'Operation report', exact: true });
  await expect(reportDialog.getByRole('region', { name: 'Report output' })).toContainText('Reviewed the current release with browser evidence.');
  const reportDownload = page.waitForEvent('download');
  await reportDialog.getByRole('button', { name: 'Download JSON' }).click();
  const download = await reportDownload;
  const report: Report = JSON.parse(await readFile((await download.path())!, 'utf8'));
  expect(report).toMatchObject({ operation_id: operation.id, mission: 'review-website', output: { summary: 'Reviewed the current release with browser evidence.' } });
  expect(download.suggestedFilename()).toBe(`${report.id}.json`);
  await reportDialog.getByRole('button', { name: 'Close', exact: true }).click();

  await page.getByRole('button', { name: 'All operations', exact: true }).click();
  const cancelled = await startOperation(page, website);
  await page.getByRole('button', { name: 'Cancel operation', exact: true }).click();
  await page.getByRole('dialog', { name: 'Cancel operation', exact: true }).getByRole('button', { name: 'Cancel operation', exact: true }).click();
  await api.operation(cancelled.id, value => value.status === 'cancelled');
  await expect(page.getByText('Operation cancelled. Already-dispatched work may still finish.', { exact: true })).toBeVisible();
});

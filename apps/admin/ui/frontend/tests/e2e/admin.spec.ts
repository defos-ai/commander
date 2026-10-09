import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect } from '@playwright/test';
import type { OperationQuestion, OperationTask } from '../../src/api/operation-types';
import type { Report } from '../../src/api/reports';
import { test, connect, startOperation, publicAPIURL, username, password } from './fixtures';

test('custom-port API catalogs survive navigation and restore server sign-in across reload', async ({ page, api }) => {
  await api.ready();
  const browserRequests: { url: string; authorization?: string }[] = [];
  page.on('request', request => browserRequests.push({ url: request.url(), authorization: request.headers().authorization }));
  const localAPIURL = 'http://localhost:4274';
  const localHealthResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.pathname === '/ui/proxy' && url.searchParams.get('url') === localAPIURL
      && url.searchParams.get('path') === '/healthz';
  });
  await page.goto('/');
  expect((await localHealthResponse).status()).toBe(502);
  await expect(page.getByText('Looking for a local API…', { exact: true })).not.toBeVisible();
  await expect(page.getByText('Local Commander detected')).not.toBeVisible();
  await page.getByRole('button', { name: 'Add connection', exact: true }).click();
  const connection = page.getByRole('dialog', { name: 'Add connection', exact: true });
  await expect(connection.getByLabel('Connection name')).toHaveValue('New connection');
  await expect(connection.getByLabel('API URL')).toHaveValue('');
  await expect(connection.getByLabel('Username', { exact: true })).toHaveValue('commander');
  await expect(connection.getByLabel('Password', { exact: true })).toHaveValue('');
  await expect(connection.getByLabel('API URL')).toBeFocused();
  await expect(connection.getByText('Need a password? Run in a terminal:', { exact: true })).not.toBeVisible();
  await connection.getByLabel('Connection name').fill('Compose test');
  const aboutResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.pathname === '/ui/proxy' && url.searchParams.get('url') === publicAPIURL
      && url.searchParams.get('path') === '/aboutz';
  });
  await connection.getByLabel('API URL').fill(publicAPIURL);
  expect((await aboutResponse).status()).toBe(200);
  await expect(connection.getByText('Need a password? Run in a terminal:', { exact: true })).toBeVisible();
  await expect(connection.getByText('docker compose exec api commander-api credentials', { exact: true })).toBeVisible();
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await connection.getByRole('button', { name: 'Copy command', exact: true }).click();
  await expect(connection.getByRole('button', { name: 'Copied', exact: true })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('docker compose exec api commander-api credentials');
  expect(browserRequests.every(request => !request.authorization)).toBe(true);
  await connection.getByLabel('Username', { exact: true }).fill(username);
  await connection.getByLabel('Password', { exact: true }).fill(password);
  await connection.getByRole('button', { name: 'Connect', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible();
  const navigation = page.getByRole('navigation', { name: 'Workspace navigation' });
  await navigation.getByRole('button', { name: 'Controllers', exact: true }).click();
  const controller = page.getByRole('button', { name: /^View controller default-controller-/ }).first();
  await expect(controller.getByText('Ready', { exact: true })).toBeVisible();
  await controller.click();
  await expect(page.getByRole('heading', { name: 'Controller snapshot', exact: true })).toBeVisible();
  await expect(page.getByText('default', { exact: true })).toBeVisible();
  await expect(page.getByText('round-robin', { exact: true })).toBeVisible();
  await expect(page.getByText('Last heartbeat', { exact: true })).toBeVisible();
  await expect(page.getByText('Lease expires at', { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/\/controllers\/default-controller-[^/]+$/);
  await navigation.getByRole('button', { name: 'Missions', exact: true }).click();
  await page.getByRole('button', { name: 'View mission review-website' }).click();
  await expect(page.getByRole('heading', { name: 'review-website', exact: true })).toBeVisible();
  const missionURL = page.url();
  await expect(page).toHaveURL(/\/missions\/review-website$/);

  const automationGraph = page.getByRole('region', { name: 'Mission automation graph' });
  await expect(automationGraph.getByRole('button', { name: /^Inspect automation / })).toHaveCount(6);
  await automationGraph.getByRole('button', { name: 'Inspect automation discover-pages', exact: true }).click();
  const automation = page.getByRole('dialog', { name: 'Automation details', exact: true });
  await expect(automation.getByRole('heading', { name: 'Automatically run proposed actions', exact: true })).toBeVisible();
  await expect(automation.getByText('Run proposals automatically only when all are allowed below. These are possible follow-ups, not mandatory steps. Failed work or no proposals also require model review.', { exact: true })).toBeVisible();
  await expect(automation.getByRole('list').getByRole('button')).toHaveCount(5);
  await expect(automation.getByText('Continue after all generated tasks finish.', { exact: true })).toBeVisible();
  await automation.getByRole('button', { name: 'capture-page', exact: true }).click();
  await expect(automation.getByRole('heading', { name: 'capture-page', exact: true })).toBeVisible();
  await expect(automation.getByRole('heading', { name: 'Model review', exact: true })).toBeVisible();
  await expect(automation.getByText('The model decides how to continue. If this action belongs to a wait-for-all group, its results are reviewed with the group.', { exact: true })).toBeVisible();
  await automation.getByRole('button', { name: 'Close dialog' }).click();
  await expect(automationGraph.getByRole('button', { name: 'Inspect automation capture-page', exact: true })).toBeFocused();

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
  expect(JSON.stringify(browserStorage)).toContain(publicAPIURL);
  expect(JSON.stringify(browserStorage)).not.toContain('api:4274');
  expect(browserRequests.every(request => !request.authorization)).toBe(true);
  expect(new Set(browserRequests.map(request => new URL(request.url).origin))).toEqual(new Set([new URL(page.url()).origin]));
  expect(browserRequests.filter(request => new URL(request.url).pathname.startsWith('/api/'))).toEqual([]);
  const proxyTargets = browserRequests.map(request => new URL(request.url))
    .filter(url => url.pathname === '/ui/proxy').map(url => url.searchParams.get('url'));
  expect(proxyTargets.length).toBeGreaterThan(0);
  expect(new Set(proxyTargets)).toEqual(new Set([localAPIURL, publicAPIURL]));
  expect(browserRequests.some(request => decodeURIComponent(request.url).includes('api:4274'))).toBe(false);

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
  await expect(signIn.getByLabel('Connection name')).toHaveValue('Compose test');
  await expect(signIn.getByLabel('API URL')).toHaveValue(publicAPIURL);
  await expect(signIn.getByLabel('Username', { exact: true })).toHaveValue('commander');
  await expect(signIn.getByLabel('Password', { exact: true })).toHaveValue('');
  await expect(signIn.getByLabel('Password', { exact: true })).toBeFocused();
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
  await page.getByRole('button', { name: 'Zoom in', exact: true }).click({ clickCount: 2 });
  await expect(page.getByLabel('Graph zoom')).toHaveText('150%');
  const drag = async (x: number, y: number) => {
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x - 80, y - 60, { steps: 8 });
    await page.mouse.up();
  };
  const rootNode = graph.getByRole('button', { name: /^Inspect Task discover-pages / });
  await rootNode.scrollIntoViewIfNeeded();
  const rootBox = (await rootNode.boundingBox())!;
  const beforePan = await graph.evaluate(element => ({ left: element.scrollLeft, top: element.scrollTop }));
  await drag(rootBox.x + rootBox.width / 2, rootBox.y + rootBox.height / 2);
  await expect.poll(() => graph.evaluate(element => element.scrollLeft)).toBeGreaterThan(beforePan.left);
  await expect.poll(() => graph.evaluate(element => element.scrollTop)).toBeGreaterThan(beforePan.top);
  await expect(page.getByRole('dialog', { name: 'Node details', exact: true })).not.toBeVisible();
  await graph.getByRole('button', { name: /^Inspect Task capture-page / }).click();
  const node = page.getByRole('region', { name: 'Node details' });
  await expect(node.getByRole('button', { name: 'Parent task discover-pages', exact: true })).toBeVisible();
  await expect(node.getByRole('button', { name: 'Produced artifact Page screenshots', exact: true })).toBeVisible();
  const captureTask = finished.tasks!.find(task => task.action_name === 'capture-page')!;
  for (const [label, value] of [['Input', captureTask.input], ['Output', captureTask.result!.output]] as const) {
    const details = node.locator('details').filter({ has: page.locator('summary').filter({ hasText: new RegExp(`^${label}$`) }) });
    const summary = details.locator('summary');
    await expect(summary).toBeVisible();
    const payload = details.locator('pre');
    await expect(payload).not.toBeVisible();
    await summary.click();
    await expect(payload).toBeVisible();
    await expect(payload).toHaveText(JSON.stringify(value, null, 2));
  }
  await page.getByRole('dialog', { name: 'Node details', exact: true }).getByRole('button', { name: 'Close dialog' }).click();
  await page.getByRole('button', { name: 'Reset view', exact: true }).click();
  await graph.scrollIntoViewIfNeeded();
  const graphBox = (await graph.boundingBox())!;
  // Start in the canvas padding and continue outside the viewport while captured.
  await drag(graphBox.x + graphBox.width - 20, graphBox.y + 12);
  await expect.poll(() => graph.evaluate(element => element.scrollLeft)).toBeGreaterThan(0);
  await expect.poll(() => graph.evaluate(element => element.scrollTop)).toBeGreaterThan(0);
  await expect(page.getByRole('dialog', { name: 'Node details', exact: true })).not.toBeVisible();
  await page.getByRole('button', { name: 'Reset view', exact: true }).click();
  await expect(page.getByLabel('Graph zoom')).toHaveText('100%');
  await expect.poll(() => graph.evaluate(element => ({ left: element.scrollLeft, top: element.scrollTop }))).toEqual({ left: 0, top: 0 });
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

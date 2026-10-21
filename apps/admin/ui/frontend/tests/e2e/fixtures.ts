import { createServer } from 'node:http';
import { test as base, expect, type APIRequestContext, type Page } from '@playwright/test';
import type { Operation } from '../../src/api/operation-types';
import type { Agent, Action, Controller, Mission } from '../../src/api/types';

export const apiURL = process.env.COMMANDER_TEST_API_URL ?? 'http://127.0.0.1:14274';
export const publicAPIURL = 'http://localhost:14274';
export const username = process.env.COMMANDER_TEST_API_USERNAME ?? 'api-fixture-user';
export const password = process.env.COMMANDER_TEST_API_PASSWORD ?? 'api-fixture-password';
const terminal = new Set(['completed', 'cancelled', 'failed']);

export class CommanderAPI {
  constructor(private readonly request: APIRequestContext) {}

  async get<T>(path: string): Promise<T> {
    const response = await this.request.get(`/api/v1${path}`);
    expect(response.status(), await response.text()).toBe(200);
    return response.json();
  }

  async post<T>(path: string, data?: unknown, status = 200): Promise<T> {
    const response = await this.request.post(`/api/v1${path}`, { data });
    expect(response.status(), await response.text()).toBe(status);
    return response.json();
  }

  async reset() {
    const response = await this.request.post('/api/v1/testing/cleanup');
    if (response.status() === 204) return;
    expect(response.status(), 'Use the isolated Compose test API with ENV=tests.').toBe(409);
    expect(await response.json()).toEqual({ error: 'test_state_busy' });
    // Only cancel saved work after proving this is the test-only cleanup endpoint.
    await this.settle();
    await expect.poll(async () => {
      const retry = await this.request.post('/api/v1/testing/cleanup');
      if (retry.status() === 409) expect(await retry.json()).toEqual({ error: 'test_state_busy' });
      return retry.status();
    }, { timeout: 30_000, message: 'Clean isolated test state' }).toBe(204);
  }

  async ready() {
    await expect.poll(async () => {
      const [agents, actions, missions, controllers] = await Promise.all([
        this.get<{ items: Agent[] }>('/agents'), this.get<{ items: Action[] }>('/actions'), this.get<{ items: Mission[] }>('/missions'),
        this.get<{ items: Controller[] }>('/controllers'),
      ]);
      // API/MCP suites can leave fixture agents running; a browser-only run has none.
      const spawned = agents.items.filter(agent => agent.type === 'fixture-agent');
      return {
        agents: agents.items.filter(agent => agent.type !== 'fixture-agent').map(agent => ({ type: agent.type, status: agent.status, stale: agent.stale, working: Boolean(agent.work),
          readyActions: agent.actions?.filter(action => action.status === 'ready').length })).sort((left, right) => left.type.localeCompare(right.type)),
        spawnedReady: spawned.every(agent => agent.status === 'ready' && !agent.stale && !agent.work
          && agent.actions?.length === 1 && agent.actions[0].name === 'fixture-echo' && agent.actions[0].status === 'ready'),
        actions: actions.items.filter(action => action.name !== 'fixture-echo').map(action => action.name).sort(),
        spawnedCatalog: actions.items.filter(action => action.name === 'fixture-echo').length === Math.min(spawned.length, 1),
        missions: missions.items.map(mission => mission.name).sort(),
        controller: controllers.items.some(controller => controller.type === 'default' && controller.status === 'ready' && !controller.stale && controller.agent_selector === 'round-robin' && controller.rule_selector === 'mission-rules'),
      };
    }, { timeout: 60_000, message: 'Wait for controller, test missions and ready web, browser, builder, spawner and any previously spawned agents' }).toEqual({
      agents: [
        { type: 'browser-agent', status: 'ready', stale: false, working: false, readyActions: 2 },
        { type: 'builder-agent', status: 'ready', stale: false, working: false, readyActions: 1 },
        { type: 'spawner-agent', status: 'ready', stale: false, working: false, readyActions: 1 },
        ...Array(2).fill({ type: 'web-agent', status: 'ready', stale: false, working: false, readyActions: 5 }),
      ], spawnedReady: true, spawnedCatalog: true,
      actions: ['build-agent', 'capture-page', 'check-accessibility', 'check-links', 'check-seo', 'checkout-template', 'detect-technologies', 'discover-pages', 'discover-pages-no-browser', 'publish-agent', 'release-agent', 'spawn-agent'],
      missions: ['build-agent', 'fixture-affinity', 'fixture-use-agent', 'install-agent', 'review-website-no-browser', 'review-website-with-browser'], controller: true,
    });
  }

  async settle() {
    await expect.poll(async () => {
      const operations = await this.get<{ items: Operation[] }>('/operations');
      for (const operation of operations.items.filter(item => !terminal.has(item.status))) {
        for (const question of operation.questions?.filter(item => item.status === 'pending') ?? []) {
          await this.settleCommand(`/operations/${operation.id}/questions/${question.id}/answer`, { answer: 'End this test review' },
            ['question_expired', 'question_answered', 'operation_finished']);
        }
        await this.settleCommand(`/operations/${operation.id}/cancel`, undefined, ['operation_finished']);
      }
      const agents = await this.get<{ items: Agent[] }>('/agents');
      return operations.items.every(item => item.progress.settled)
        && agents.items.every(agent => !agent.work && agent.status !== 'working');
    }, { timeout: 60_000, message: 'Settle test operations before removing stored state' }).toBe(true);
  }

  private async settleCommand(path: string, data: unknown, allowedConflicts: string[]) {
    const response = await this.request.post(`/api/v1${path}`, { data });
    if (response.status() === 200) return;
    expect(response.status(), await response.text()).toBe(409);
    expect(allowedConflicts).toContain((await response.json()).error);
  }

  async operation(id: string, predicate: (operation: Operation) => boolean): Promise<Operation> {
    let operation: Operation | undefined;
    await expect.poll(async () => {
      operation = await this.get<Operation>(`/operations/${id}`);
      return predicate(operation);
    }, { timeout: 90_000, message: `Wait for operation ${id}` }).toBe(true);
    return operation!;
  }
}

export const test = base.extend<{ api: CommanderAPI; website: string }>({
  api: async ({ playwright }, use) => {
    const request = await playwright.request.newContext({
      baseURL: apiURL,
      extraHTTPHeaders: { Authorization: `Basic ${Buffer.from(`${username}:${password}`).toString('base64')}` },
      timeout: 15_000,
    });
    const api = new CommanderAPI(request);
    let initialized = false;
    try {
      await api.reset();
      initialized = true;
      await api.ready();
      await use(api);
    } finally {
      try { if (initialized) { await api.settle(); await api.reset(); } }
      finally { await request.dispose(); }
    }
  },
  website: async ({}, use) => {
    const server = createServer((request, response) => {
      if (request.url === '/missing') { response.writeHead(404); response.end('Not found'); return; }
      response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'X-Powered-By': 'Express' });
      response.end('<!doctype html><html lang="en"><head><title>Release review fixture</title><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="description" content="A local release review fixture."></head><body><main><h1>Release review</h1><p>A local page for Commander browser tests.</p><a href="/missing">Broken link</a></main></body></html>');
    });
    await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '0.0.0.0', resolve); });
    const address = server.address();
    if (!address || typeof address === 'string') throw new Error('Website fixture has no TCP address');
    try { await use(`http://${process.env.COMMANDER_TEST_WEBSITE_HOST ?? 'host.docker.internal'}:${address.port}`); }
    finally {
      server.closeAllConnections();
      await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
    }
  },
});

export async function connect(page: Page, connection = { name: 'Compose test', url: publicAPIURL }) {
  await page.goto('/');
  await page.getByRole('button', { name: 'Add connection', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Add connection', exact: true });
  await dialog.getByLabel('Connection name').fill(connection.name);
  await dialog.getByLabel('API URL').fill(connection.url);
  await dialog.getByLabel('Username', { exact: true }).fill(username);
  await dialog.getByLabel('Password', { exact: true }).fill(password);
  await dialog.getByRole('button', { name: 'Connect', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible();
}

export async function startOperation(page: Page, website: string): Promise<Operation> {
  await page.getByRole('button', { name: 'Start operation', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Start operation', exact: true });
  await dialog.getByLabel('Mission', { exact: true }).selectOption('review-website-with-browser');
  await dialog.getByLabel('Mission input (JSON)').fill(JSON.stringify({ url: website, max_pages: 1 }));
  const response = page.waitForResponse(reply => new URL(reply.url()).searchParams.get('path') === '/api/v1/operations' && reply.request().method() === 'POST');
  await dialog.getByRole('button', { name: 'Start operation', exact: true }).click();
  const result = await response;
  expect(result.status()).toBe(201);
  await expect(dialog).not.toBeVisible();
  return result.json();
}

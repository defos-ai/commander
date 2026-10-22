import { randomUUID } from 'node:crypto';
import { expect } from '@playwright/test';
import type { Mission } from '../../src/api/types';
import { test, connect } from './fixtures';

test('create a reusable mission through Admin without installed actions or starting work', async ({ page, api }) => {
  const mission = {
    name: `ui-mission-${randomUUID()}`,
    objectives: ['Review using an action that has not been installed yet.', 'Save findings for a later review.'],
    required_actions: ['future-mission-action'],
    starting_actions: ['future-mission-action'],
    action_definitions: [{ name: 'future-mission-action', description: 'Review a future capability.',
      input_schema: { type: 'object' }, output_schema: { type: 'object', properties: { summary: { type: 'string' } } },
      required_secrets: ['REVIEW_TOKEN'] }],
    input_schema: {},
    output_schema: {},
  };
  const definition = JSON.stringify(mission, null, 2).replace(/^  "input_schema": \{\}/m,
    String.raw`  "input_schema": {"type":"object","properties":{"exact":{"const":9007199254740993},"escaped":{"const":"\u0061\\path"}}}`);
  const expectedMission = { ...mission, objectives: [mission.objectives[0], 'Keep evidence for a later review.'], optional_actions: ['check-seo'] };
  const operationIDs = async () => (await api.get<{ items: { id: string }[] }>('/operations')).items.map(item => item.id).sort();
  const actionNames = async () => (await api.get<{ items: { name: string }[] }>('/actions')).items.map(item => item.name).sort();
  const before = await operationIDs();
  const actionsBefore = await actionNames();
  await connect(page);
  await page.getByRole('navigation', { name: 'Workspace navigation' }).getByRole('button', { name: 'Missions', exact: true }).click();
  const missionsURL = page.url();
  await page.getByRole('button', { name: 'Create mission', exact: true }).click();
  const create = page.getByRole('main');
  await expect(page).toHaveURL(/\/create-mission$/);
  const creationURL = page.url();
  await expect(create.getByRole('heading', { name: 'Create mission', exact: true })).toBeVisible();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(create.getByRole('textbox', { name: 'Mission name', exact: true })).toBeFocused();
  await expect(page.getByRole('navigation', { name: 'Workspace navigation' }).getByRole('button', { name: 'Missions', exact: true })).toHaveAttribute('aria-current', 'page');
  await create.getByRole('textbox', { name: 'Mission name', exact: true }).fill('unsaved-draft');
  await create.getByRole('button', { name: 'All missions', exact: true }).click();
  await expect(page).toHaveURL(missionsURL);
  await page.goBack();
  await expect(page).toHaveURL(creationURL);
  await expect(create.getByRole('textbox', { name: 'Mission name', exact: true })).toHaveValue('');
  await page.reload();
  await expect(page).toHaveURL(creationURL);
  await expect(create.getByRole('heading', { name: 'Create mission', exact: true })).toBeVisible();
  await create.getByRole('button', { name: 'JSON', exact: true }).click();
  await create.getByLabel('Mission definition (JSON)', { exact: true }).fill(definition);
  await create.getByRole('button', { name: 'Form', exact: true }).click();
  await expect(create.getByRole('textbox', { name: 'Mission name', exact: true })).toHaveValue(mission.name);
  await create.getByRole('textbox', { name: 'Objective 2', exact: true }).fill(expectedMission.objectives[1]);
  await create.getByRole('combobox', { name: 'Role for check-seo', exact: true }).selectOption('optional');
  const inspector = create.locator('summary').filter({ hasText: /^Inspect check-seo$/ }).locator('..');
  await inspector.locator('summary').first().click();
  const reports = inspector.getByRole('heading', { name: 'Agent reports', exact: true }).locator('..');
  const webAgents = (await api.get<{ items: { id: string; type: string }[] }>('/agents')).items.filter(agent => agent.type === 'web-agent');
  expect(webAgents).toHaveLength(2);
  for (const agent of webAgents) {
    const report = reports.getByRole('listitem').filter({ has: page.getByText(agent.id, { exact: true }) });
    await expect(report.getByText('Agent status', { exact: true })).toBeVisible();
    const actionReadiness = report.getByText('Reported action readiness', { exact: true }).locator('..');
    await expect(actionReadiness.getByText('Ready', { exact: true })).toBeVisible();
  }
  await create.getByRole('button', { name: 'JSON', exact: true }).click();
  await expect(create.getByLabel('Mission definition (JSON)', { exact: true })).toHaveValue(/9007199254740993/);
  await create.getByRole('button', { name: 'Create mission', exact: true }).click();
  await expect(create.getByRole('textbox', { name: 'Mission definition (JSON)', exact: true })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: mission.name, exact: true })).toBeVisible();
  const objectives = page.getByRole('heading', { name: 'Objectives', exact: true }).locator('..').getByRole('list');
  await expect(objectives).toHaveClass('catalog-tags');
  await expect(objectives.getByRole('listitem')).toHaveText(expectedMission.objectives);
  const missionURL = page.url();
  await page.reload();
  await expect(page).toHaveURL(missionURL);
  await expect(page.getByRole('heading', { name: mission.name, exact: true })).toBeVisible();
  expect(await api.get<Mission>(`/missions/${mission.name}`)).toMatchObject(expectedMission);
  await page.getByRole('group', { name: 'Mission view', exact: true }).getByRole('button', { name: 'JSON', exact: true }).click();
  const jsonURL = `${missionURL}/json`;
  await expect(page).toHaveURL(jsonURL);
  const savedDefinition = page.getByRole('textbox', { name: 'Mission definition (JSON)', exact: true });
  await expect(savedDefinition).toHaveAttribute('readonly');
  const savedJSON = await savedDefinition.inputValue();
  expect(JSON.parse(savedJSON).action_definitions).toEqual(mission.action_definitions);
  expect(savedJSON).toContain('9007199254740993');
  expect(savedJSON).toContain(String.raw`"\u0061\\path"`);
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.getByRole('button', { name: 'Copy JSON', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Copied', exact: true })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(savedJSON);
  await page.reload();
  await expect(page).toHaveURL(jsonURL);
  await expect(savedDefinition).toHaveValue(savedJSON);
  await page.getByRole('button', { name: 'Details', exact: true }).click();
  await expect(page).toHaveURL(missionURL);

  await page.getByRole('button', { name: 'All missions', exact: true }).click();
  await expect(page.getByRole('button', { name: `View mission ${mission.name}`, exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Create mission', exact: true }).click();
  await create.getByRole('button', { name: 'JSON', exact: true }).click();
  await create.getByLabel('Mission definition (JSON)', { exact: true }).fill(JSON.stringify({ ...expectedMission, objectives: ['Attempt to overwrite the saved mission.'] }, null, 2));
  await create.getByRole('button', { name: 'Create mission', exact: true }).click();
  await expect(create.getByRole('alert')).toContainText('A mission with this name already exists');
  await create.getByRole('button', { name: 'View existing mission', exact: true }).click();
  await expect(create.getByRole('textbox', { name: 'Mission definition (JSON)', exact: true })).toHaveCount(0);
  await expect(page).toHaveURL(missionURL);
  await expect(page.getByRole('heading', { name: mission.name, exact: true })).toBeVisible();
  expect(await api.get<Mission>(`/missions/${mission.name}`)).toMatchObject(expectedMission);
  expect(await operationIDs()).toEqual(before);
  expect(await actionNames()).toEqual(actionsBefore);
});

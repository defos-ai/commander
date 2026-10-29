import { expect } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import type { Mission } from '../../src/api/types';
import { connect, test } from '../fixtures/e2e';

test('create and edit a reusable mission through Admin without installing actions or starting work', async ({ page, api }) => {
  const actionInputSchema = String.raw`{"type":"object","properties":{"exact":{"const":9007199254740993},"escaped":{"const":"\u0061\\path"}}}`;
  const missionInputSchema = String.raw`{"type":"object","properties":{"exact":{"const":9007199254740993},"escaped":{"const":"\u0061\\path"}}}`;
  const mission = {
    name: `ui-mission-${randomUUID()}`,
    objectives: ['Review using an action that has not been installed yet.', 'Save findings for a later review.'],
    required_actions: ['future-mission-action'],
    starting_actions: ['future-mission-action'],
    action_definitions: [{ name: 'future-mission-action', description: 'Review a future capability.',
      input_schema: JSON.parse(actionInputSchema), output_schema: { type: 'object', properties: { summary: { type: 'string' } } },
      required_secrets: ['REVIEW_TOKEN'] }],
    input_schema: JSON.parse(missionInputSchema),
    output_schema: {},
  };
  const expectedMission = { ...mission, objectives: [mission.objectives[0], 'Keep evidence for a later review.'], optional_actions: ['analyze-text'] };
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
  await create.getByRole('textbox', { name: 'Mission name', exact: true }).fill(mission.name);
  await create.getByRole('textbox', { name: 'Objective 1', exact: true }).fill(mission.objectives[0]);
  await create.getByRole('button', { name: 'Add objective', exact: true }).click();
  await create.getByRole('textbox', { name: 'Objective 2', exact: true }).fill(expectedMission.objectives[1]);
  const define = create.getByRole('button', { name: 'Define action', exact: true });
  await define.click();
  const definitionDialog = page.getByRole('dialog', { name: 'Define action', exact: true });
  await expect(definitionDialog.getByRole('textbox', { name: 'Action name', exact: true })).toBeFocused();
  await definitionDialog.getByRole('textbox', { name: 'Action name', exact: true }).fill('cancelled-action');
  await definitionDialog.press('Escape');
  await expect(definitionDialog).toHaveCount(0);
  await expect(define).toBeFocused();
  await expect(create.getByRole('combobox', { name: 'Role for cancelled-action', exact: true })).toHaveCount(0);
  await define.click();
  await definitionDialog.getByRole('textbox', { name: 'Action name', exact: true }).fill('future-mission-action');
  await definitionDialog.getByRole('textbox', { name: 'Description', exact: true }).fill(mission.action_definitions[0].description);
  await definitionDialog.getByRole('textbox', { name: 'Action input schema (JSON)', exact: true }).fill(actionInputSchema);
  await definitionDialog.getByRole('textbox', { name: 'Action output schema (JSON)', exact: true }).fill(JSON.stringify(mission.action_definitions[0].output_schema));
  await definitionDialog.getByRole('textbox', { name: 'Required secret names', exact: true }).fill('REVIEW_TOKEN');
  await definitionDialog.getByRole('button', { name: 'Add action', exact: true }).click();
  await expect(definitionDialog).toHaveCount(0);
  await expect(create.getByRole('heading', { name: 'Create mission', exact: true })).toBeVisible();
  await expect(create.getByRole('combobox', { name: 'Role for future-mission-action', exact: true })).toHaveValue('required');
  await expect(create.getByRole('checkbox', { name: 'Start with future-mission-action', exact: true })).not.toBeChecked();
  await create.getByRole('checkbox', { name: 'Start with future-mission-action', exact: true }).check();
  await expect(create.getByText('Defined in this mission', { exact: true })).toBeVisible();
  await create.locator('summary').filter({ hasText: /^Advanced settings$/ }).click();
  await create.getByRole('textbox', { name: 'Input schema (JSON)', exact: true }).fill(missionInputSchema);
  await create.getByRole('searchbox', { name: 'Search actions', exact: true }).fill('');
  await create.getByRole('combobox', { name: 'Role for analyze-text', exact: true }).selectOption('optional');
  const inspector = create.locator('summary').filter({ hasText: /^Inspect analyze-text$/ }).locator('..');
  await inspector.locator('summary').first().click();
  const reports = inspector.getByRole('heading', { name: 'Agent reports', exact: true }).locator('..');
  const exampleAgents = (await api.get<{ items: { id: string; type: string }[] }>('/agents')).items.filter(agent => agent.type === 'example-agent');
  expect(exampleAgents).toHaveLength(2);
  for (const agent of exampleAgents) {
    const report = reports.getByRole('listitem').filter({ has: page.getByText(agent.id, { exact: true }) });
    await expect(report.getByText('Agent status', { exact: true })).toBeVisible();
    const actionReadiness = report.getByText('Reported action readiness', { exact: true }).locator('..');
    await expect(actionReadiness.getByText('Ready', { exact: true })).toBeVisible();
  }
  await create.getByRole('button', { name: 'JSON', exact: true }).click();
  const authoredJSON = await create.getByLabel('Mission definition (JSON)', { exact: true }).inputValue();
  expect(authoredJSON.match(/9007199254740993/g)).toHaveLength(2);
  expect(authoredJSON).toContain(String.raw`"\u0061\\path"`);
  await create.getByRole('button', { name: 'Form', exact: true }).click();
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
  expect(savedJSON.match(/9007199254740993/g)).toHaveLength(2);
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
  await page.getByRole('button', { name: 'Edit mission', exact: true }).click();
  await expect(page).toHaveURL(`${missionURL}/edit`);
  await expect(create.getByRole('textbox', { name: 'Mission name', exact: true })).toHaveValue(mission.name);
  await expect(create.getByRole('textbox', { name: 'Mission name', exact: true })).toHaveAttribute('readonly');
  await create.getByRole('textbox', { name: 'Objective 2', exact: true }).fill('Report release findings.');
  await create.getByRole('combobox', { name: 'Role for analyze-text', exact: true }).selectOption('required');
  await create.locator('summary').filter({ hasText: /^Advanced settings$/ }).click();
  await create.getByRole('textbox', { name: 'Output schema (JSON)', exact: true }).fill('{"type":"object","properties":{"summary":{"type":"string"}}}');
  await create.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page).toHaveURL(missionURL);
  await expect(page.getByRole('heading', { name: mission.name, exact: true })).toBeVisible();
  const updated = await api.get<Mission>(`/missions/${mission.name}`);
  expect(updated).toMatchObject({ ...mission, objectives: [expectedMission.objectives[0], 'Report release findings.'],
    required_actions: ['future-mission-action', 'analyze-text'], output_schema: { type: 'object', properties: { summary: { type: 'string' } } } });
  expect(updated.optional_actions ?? []).toEqual([]);
  await page.getByRole('group', { name: 'Mission view', exact: true }).getByRole('button', { name: 'JSON', exact: true }).click();
  const updatedJSON = await page.getByRole('textbox', { name: 'Mission definition (JSON)', exact: true }).inputValue();
  expect(updatedJSON.match(/9007199254740993/g)).toHaveLength(2);
  expect(updatedJSON).toContain(String.raw`"\u0061\\path"`);
  expect(await operationIDs()).toEqual(before);
  expect(await actionNames()).toEqual(actionsBefore);
});

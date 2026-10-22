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
    input_schema: {},
    output_schema: {},
  };
  const operationIDs = async () => (await api.get<{ items: { id: string }[] }>('/operations')).items.map(item => item.id).sort();
  const before = await operationIDs();
  await connect(page);
  await page.getByRole('navigation', { name: 'Workspace navigation' }).getByRole('button', { name: 'Missions', exact: true }).click();
  const missionsURL = page.url();
  await page.getByRole('button', { name: 'Create mission', exact: true }).click();
  const create = page.getByRole('main');
  await expect(page).toHaveURL(/\/create-mission$/);
  const creationURL = page.url();
  await expect(create.getByRole('heading', { name: 'Create mission', exact: true })).toBeVisible();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(create.getByLabel('Mission definition (JSON)', { exact: true })).toBeFocused();
  await expect(page.getByRole('navigation', { name: 'Workspace navigation' }).getByRole('button', { name: 'Missions', exact: true })).toHaveAttribute('aria-current', 'page');
  await create.getByLabel('Mission definition (JSON)', { exact: true }).fill(JSON.stringify({ ...mission, name: 'unsaved-draft' }));
  await create.getByRole('button', { name: 'All missions', exact: true }).click();
  await expect(page).toHaveURL(missionsURL);
  await page.goBack();
  await expect(page).toHaveURL(creationURL);
  await expect(create.getByLabel('Mission definition (JSON)', { exact: true })).not.toHaveValue(/unsaved-draft/);
  await page.reload();
  await expect(page).toHaveURL(creationURL);
  await expect(create.getByRole('heading', { name: 'Create mission', exact: true })).toBeVisible();
  await create.getByLabel('Mission definition (JSON)', { exact: true }).fill(JSON.stringify(mission, null, 2));
  await create.getByRole('button', { name: 'Create mission', exact: true }).click();
  await expect(create.getByRole('textbox', { name: 'Mission definition (JSON)', exact: true })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: mission.name, exact: true })).toBeVisible();
  const objectives = page.getByRole('heading', { name: 'Objectives', exact: true }).locator('..').getByRole('list');
  await expect(objectives).toHaveClass('catalog-tags');
  await expect(objectives.getByRole('listitem')).toHaveText(mission.objectives);
  const missionURL = page.url();
  await page.reload();
  await expect(page).toHaveURL(missionURL);
  await expect(page.getByRole('heading', { name: mission.name, exact: true })).toBeVisible();
  expect(await api.get<Mission>(`/missions/${mission.name}`)).toMatchObject(mission);

  await page.getByRole('button', { name: 'All missions', exact: true }).click();
  await expect(page.getByRole('button', { name: `View mission ${mission.name}`, exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Create mission', exact: true }).click();
  await create.getByLabel('Mission definition (JSON)', { exact: true }).fill(JSON.stringify({ ...mission, objectives: ['Attempt to overwrite the saved mission.'] }, null, 2));
  await create.getByRole('button', { name: 'Create mission', exact: true }).click();
  await expect(create.getByRole('alert')).toContainText('A mission with this name already exists');
  await create.getByRole('button', { name: 'View existing mission', exact: true }).click();
  await expect(create.getByRole('textbox', { name: 'Mission definition (JSON)', exact: true })).toHaveCount(0);
  await expect(page).toHaveURL(missionURL);
  await expect(page.getByRole('heading', { name: mission.name, exact: true })).toBeVisible();
  expect(await api.get<Mission>(`/missions/${mission.name}`)).toMatchObject(mission);
  expect(await operationIDs()).toEqual(before);
});

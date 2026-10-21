import { randomUUID } from 'node:crypto';
import { expect } from '@playwright/test';
import type { Mission } from '../../src/api/types';
import { test, connect } from './fixtures';

test('create a reusable mission through Admin without installed actions or starting work', async ({ page, api }) => {
  const mission = {
    name: `ui-mission-${randomUUID()}`,
    objective: 'Review using an action that has not been installed yet.',
    required_actions: ['future-mission-action'],
    starting_actions: ['future-mission-action'],
    input_schema: {},
    output_schema: {},
  };
  const operationIDs = async () => (await api.get<{ items: { id: string }[] }>('/operations')).items.map(item => item.id).sort();
  const before = await operationIDs();
  await connect(page);
  await page.getByRole('navigation', { name: 'Workspace navigation' }).getByRole('button', { name: 'Missions', exact: true }).click();
  await page.getByRole('button', { name: 'Create mission', exact: true }).click();
  const create = page.getByRole('dialog', { name: 'Create mission', exact: true });
  await create.getByLabel('Mission definition (JSON)', { exact: true }).fill(JSON.stringify(mission, null, 2));
  await create.getByRole('button', { name: 'Create mission', exact: true }).click();
  await expect(create).not.toBeVisible();
  await expect(page.getByRole('heading', { name: mission.name, exact: true })).toBeVisible();
  const missionURL = page.url();
  await page.reload();
  await expect(page).toHaveURL(missionURL);
  await expect(page.getByRole('heading', { name: mission.name, exact: true })).toBeVisible();
  expect(await api.get<Mission>(`/missions/${mission.name}`)).toMatchObject(mission);

  await page.getByRole('button', { name: 'All missions', exact: true }).click();
  await expect(page.getByRole('button', { name: `View mission ${mission.name}`, exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Create mission', exact: true }).click();
  await create.getByLabel('Mission definition (JSON)', { exact: true }).fill(JSON.stringify({ ...mission, objective: 'Attempt to overwrite the saved mission.' }, null, 2));
  await create.getByRole('button', { name: 'Create mission', exact: true }).click();
  await expect(create.getByRole('alert')).toContainText('A mission with this name already exists');
  await create.getByRole('button', { name: 'View existing mission', exact: true }).click();
  await expect(create).not.toBeVisible();
  await expect(page).toHaveURL(missionURL);
  await expect(page.getByRole('heading', { name: mission.name, exact: true })).toBeVisible();
  expect(await api.get<Mission>(`/missions/${mission.name}`)).toMatchObject(mission);
  expect(await operationIDs()).toEqual(before);
});

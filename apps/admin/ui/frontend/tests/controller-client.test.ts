import { describe, expect, it } from 'vitest';
import { getController, listControllers } from '../src/api/controllers';
import { respondSequence as respond } from './fakes/fetch';
import { controller, credentials, url } from './fixtures/controller-client';

describe('controller catalog client', () => {
  it('loads snapshots through the authenticated same-origin proxy and preserves base paths and encoded IDs', async () => {
    const id = 'team/controller ?#é';
    const fetch = respond({ items: [controller] }, { ...controller, id });

    const items = await listControllers(url, credentials);
    const detail = await getController(`${url}/`, credentials, id);

    expect(items).toEqual([controller]);
    expect(detail).toEqual({ ...controller, id });
    expect(fetch.mock.calls.map(([input]) => {
      const endpoint = new URL(String(input), window.location.origin);
      expect(endpoint.origin).toBe(window.location.origin);
      expect(endpoint.pathname).toBe('/ui/proxy');
      return Object.fromEntries(endpoint.searchParams);
    })).toEqual([
      { url, path: '/api/v1/controllers' },
      { url, path: '/api/v1/controllers/team%2Fcontroller%20%3F%23%C3%A9' },
    ]);
  });

  it('accepts empty catalogs and future statuses and reports status reasons', async () => {
    const future = { ...controller, status: 'recovering', status_reason: 'Waiting for broker', agent_selector: 'custom-policy', rule_selector: 'custom-rules' };
    respond({ items: [] }, future);
    expect(await listControllers(url, credentials)).toEqual([]);
    expect(await getController(url, credentials, controller.id)).toEqual(future);
  });

  it('rejects malformed identifiers before requests and mismatched detail identities', async () => {
    const fetch = respond({ ...controller, id: 'another-controller' });
    for (const id of ['', ' ', '.', '..', '\ud800']) {
      await expect(getController(url, credentials, id)).rejects.toMatchObject({ kind: 'invalid_response' });
    }
    expect(fetch).not.toHaveBeenCalled();
    await expect(getController(url, credentials, controller.id)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it.each([
    { id: '' }, { type: null }, { status: 42 }, { stale: 'false' }, { status_reason: null },
    { server: null }, { server: { hostname: 'host', os: 'linux', arch: 64 } },
    { version: null }, { agent_selector: [] }, { rule_selector: undefined }, { rule_selector: [] }, { started_at: 'invalid' },
    { last_heartbeat: 42 }, { lease_expires_at: undefined },
  ])('rejects malformed snapshots: %j', async patch => {
    respond({ items: [{ ...controller, ...patch }] });
    await expect(listControllers(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
  });

  it.each([{}, { items: null }, []])('rejects missing list envelopes: %j', async body => {
    respond(body);
    await expect(listControllers(url, credentials)).rejects.toMatchObject({ kind: 'invalid_response' });
  });
});

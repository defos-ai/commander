import type { Route } from '@playwright/test';
import type { Agent } from '../../src/api/types';

export interface AgentDependencySnapshots {
  blocked: Agent;
  ready: Agent;
  legacy: Agent;
}

export class AgentDependencyResponses {
  private available = false;
  private readonly snapshots: AgentDependencySnapshots;

  constructor(snapshots: AgentDependencySnapshots) {
    this.snapshots = structuredClone(snapshots);
  }

  restore() { this.available = true; }

  handle = async (route: Route) => {
    if (route.request().method() !== 'GET') { await route.fallback(); return; }
    const response = await route.fetch();
    if (!response.ok()) { await route.fulfill({ response }); return; }
    const path = new URL(route.request().url()).searchParams.get('path');
    if (path === '/api/v1/agents') {
      const saved: { items: Agent[] } = await response.json();
      await route.fulfill({ response, json: { ...saved, items: saved.items.map(agent => this.project(agent)) } });
    } else {
      await route.fulfill({ response, json: this.project(await response.json()) });
    }
  };

  private project(agent: Agent): Agent {
    const snapshot = agent.id === this.snapshots.blocked.id
      ? this.available ? this.snapshots.ready : this.snapshots.blocked
      : agent.id === this.snapshots.legacy.id ? this.snapshots.legacy : undefined;
    return snapshot ? { ...agent, version: snapshot.version, dependencies: snapshot.dependencies, actions: snapshot.actions } : agent;
  }
}

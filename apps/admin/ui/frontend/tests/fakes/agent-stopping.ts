import type { Route } from '@playwright/test';
import type { Agent } from '../../src/api/types';

interface StopReply {
  status: number;
  body: unknown;
  saved: Agent[];
  resume?: Promise<void>;
}

export class AgentStoppingResponses {
  private readonly agents = new Map<string, Agent>();
  private readonly replies: StopReply[] = [];
  private readonly writes: unknown[] = [];

  constructor(agents: Agent[]) {
    for (const agent of agents) this.agents.set(agent.id, structuredClone(agent));
  }

  get submissions() { return structuredClone(this.writes); }

  respondOnce(status: number, body: unknown, saved: Agent[] = []) {
    this.replies.push(structuredClone({ status, body, saved }));
  }

  holdResponse(status: number, body: unknown, saved: Agent[] = []) {
    let resume!: () => void;
    const released = new Promise<void>(resolve => { resume = resolve; });
    this.replies.push({ ...structuredClone({ status, body, saved }), resume: released });
    return resume;
  }

  save(agent: Agent) { this.agents.set(agent.id, structuredClone(agent)); }

  handle = async (route: Route) => {
    const path = new URL(route.request().url()).searchParams.get('path');
    if (route.request().method() === 'POST' && path === '/api/v1/agents/stop') {
      this.writes.push(route.request().postDataJSON());
      const reply = this.replies.shift();
      if (!reply) throw new Error('Unexpected repeated agent stop command.');
      for (const agent of reply.saved) this.save(agent);
      if (reply.resume) {
        let deadline: ReturnType<typeof setTimeout> | undefined;
        try { await Promise.race([reply.resume, new Promise<void>(resolve => { deadline = setTimeout(resolve, 10_000); })]); }
        finally { clearTimeout(deadline); }
      }
      await route.fulfill({ status: reply.status, json: reply.body });
      return;
    }
    if (route.request().method() !== 'GET') { await route.fallback(); return; }
    const response = await route.fetch();
    if (!response.ok()) { await route.fulfill({ response }); return; }
    if (path === '/api/v1/agents') {
      const body: { items: Agent[] } = await response.json();
      await route.fulfill({ response, json: { ...body, items: body.items.map(agent => this.project(agent)) } });
    } else {
      await route.fulfill({ response, json: this.project(await response.json()) });
    }
  };

  private project(agent: Agent): Agent {
    const saved = this.agents.get(agent.id);
    return saved ? { ...agent, ...structuredClone(saved) } : agent;
  }
}

import type { Agent } from '../../../src/api/types';
import type { AgentDependencySnapshots } from '../../fakes/agent-dependencies';

export function agentDependencySnapshots(agent: Agent, legacy: Agent): AgentDependencySnapshots {
  const action = agent.actions?.[0];
  if (!action) throw new Error('Agent dependency browser fixture needs a reported action.');
  const blockedAction = {
    ...action, status: 'unavailable', status_reason: 'Host prerequisites are missing.',
    required_secrets: [...new Set([...(action.required_secrets ?? []), 'fixture-runtime'])],
    missing_requirements: [
      { kind: 'executable', name: 'fixture-runtime' },
      { kind: 'configuration', name: 'fixture-runtime' },
      { kind: 'secret', name: 'fixture-runtime' },
      { kind: 'executable', name: 'Fixture-runtime' },
    ],
  };
  const blocked: Agent = {
    ...agent,
    version: 'v7.8.9',
    dependencies: [
      { name: 'fixture-runtime', description: 'Install the runtime for this host platform.', version: 'v1.2.3' },
      { name: 'helper-runtime', description: 'Auxiliary software used by optional actions.' },
    ],
    actions: [blockedAction, ...agent.actions!.slice(1)],
  };
  return {
    blocked,
    ready: { ...blocked, actions: [{ ...blockedAction, status: 'ready', status_reason: undefined, missing_requirements: [] }, ...agent.actions!.slice(1)] },
    legacy: { ...legacy, version: undefined, dependencies: undefined },
  };
}

export function stoppingAgentSnapshots(agent: Agent, other: Agent) {
  const ready = {
    ...agent, version: 'v8.9.0', status: 'ready', stale: false, work: null,
    server: { ...agent.server, hostname: 'selected-host' },
  };
  const requested = {
    ...ready, status: 'stop_requested',
    stop_command_id: 'a1234567-1234-4123-8123-123456789abc',
    stop_requested_at: new Date().toISOString(),
  };
  return {
    ready,
    other: {
      ...other, version: ready.version, status: 'ready', stale: false, work: null,
      server: { ...other.server, hostname: 'other-host' },
    },
    requested,
    wrongHostRequested: { ...requested, server: { ...requested.server, hostname: 'other-host' } },
    stopped: { ...requested, status: 'stopped' },
  };
}

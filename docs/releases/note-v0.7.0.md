# Release notes v0.7.0

## Commander v0.7.0

Inspect running agent versions and gracefully stop instances before installing
replacements.

- Report each agent's optional package version through saved inventory, the
  HTTP API, MCP and Admin. Admin displays **Unknown** for unreported versions
  and includes versions in agent search.
- Stop agents through `POST /api/v1/agents/stop`, MCP `stop_agent`, or Admin's
  individual and group controls. Exact ID, type and version filters intersect;
  the controller captures the matching instances once and returns per-agent
  outcomes, including partial failures.
- Block new assignments after accepting a stop while previously accepted work
  drains. Preserve task results and artifacts; distinguish `stop_requested`
  from confirmed `stopped`. Pending stops survive controller restart and lease
  expiry without treating a timeout or stale heartbeat as shutdown.
- Require explicit confirmation and saved-state readback in Admin. Interrupted
  or uncertain requests remain visible across dialog dismissal and list/detail
  navigation, without automatic command retries.
- Document the stop-and-replace workflow: install a published pinned release in
  a new operation, then verify its fresh ID, reported version and action readiness.

Upgrade the controller, API, MCP and storage providers together. Custom
`AgentStore` implementations must add `RequestStop`; custom controller command
clients must implement `StopAgents`. External agents can report their own package
version through `AgentConfig.Version`; rebuild them against the updated framework
when adopting that field. Unstamped agents retain an unreported version.

Keep saved data; no reset or migration is required. Stopping leaves container/Pod
removal and restart policies under deployment control, and does not move
agent-owned sessions to replacements.

[Agent lifecycle guide](https://github.com/defos-ai/commander/blob/v0.7.0/docs/features/agents.md) ·
[Agent packaging](https://github.com/defos-ai/commander/blob/v0.7.0/docs/agent-packages.md) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.6.1...v0.7.0)

## Commander builder agent v0.7.0

- Report the builder package version from stamped native, Docker and embedded
  builds. Unstamped local builds leave the version unreported.
- Adopt Commander v0.7.0's controller-managed stopping: accepted builds can drain
  and retain their results and source artifacts before shutdown is confirmed.
- Keep existing workspace data and managed credentials. Replacing a builder
  creates a new owner; workspace sessions are not transferred automatically.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.7.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.7.0/apps/agents/builder/README.md)

## Commander spawner agent v0.7.0

- Report the spawner package version from stamped native, Docker and embedded
  builds, independently of the enclosing host's version.
- Adopt Commander v0.7.0's controller-managed graceful stopping. Keep execution
  records while children exist and inspect uncertain launches before requesting
  another instance.
- Install replacements through a new `install-agent` operation using a pinned
  package. Verify each new ID, reported version and action readiness. Existing
  installation records need no reset; Docker and Kubernetes child cleanup remains
  an operator responsibility.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.7.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.7.0/apps/agents/spawner/README.md) ·
[Stop and replace agents](https://github.com/defos-ai/commander/blob/v0.7.0/docs/features/agents.md#replace-an-agent)

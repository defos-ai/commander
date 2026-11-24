# Release notes v0.8.0

## Commander v0.8.0

Choose where actions run with an optional hostname filter, while the controller
keeps responsibility for selecting an eligible agent.

- HTTP task assignments and MCP `assign_task` accept
  `"selector":{"hostname":"worker-host"}` beside `action_name` and `input`.
  The controller filters action-compatible agents by hostname, readiness,
  capacity and session ownership, then applies its selection strategy
  (RoundRobin by default). No match leaves the task queued.
- Same-action direct follow-ups, including batch launches and model-resolved
  continuations, inherit the saved hostname filter. Different-action children
  use normal selection. Recovery preserves assigned agent and command identities.
- Task and operation reads expose the saved filter separately from the assigned
  agent ID. Admin shows it in task details and execution graph dialogs.
- Agent stop selectors now support hostname alongside exact ID, type and
  reported version. Admin group-stop controls expose all four criteria. Stop
  acceptance remains distinct from confirmed shutdown.
- Framework integrations use `operations.TaskSelector` for hostname-only task
  targeting and `agents.StopSelector` for broad stop criteria. The existing
  controller `AgentSelector` strategy interface remains unchanged.

Update custom Go stop integrations from `agents.Selector` to `agents.StopSelector`.
Task selectors accept only hostname; ID, type and version are rejected on task
assignments. The action name already establishes the required capability.
Existing v0.7.1 operations without a selector remain unconstrained and need no
saved-state migration. Agent action inputs do not change for this feature.

Use distinct advertised hostnames to distinguish runtime targets. A container or
Kubernetes Pod hostname identifies that execution namespace, not a physical host
or Kubernetes node. Select the spawner to choose its runtime, then use each
launch result's child `agent_id` for exact stop requests.

[Targeting and stopping agents](https://github.com/defos-ai/commander/blob/v0.8.0/docs/features/agents.md) ·
[Operation routing](https://github.com/defos-ai/commander/blob/v0.8.0/docs/features/operations.md#choose-an-executor) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.7.1...v0.8.0)

## Commander builder agent v0.8.0

- Align Commander module requirements with v0.8.0.
- Keep existing action inputs, workspace sessions and managed-secret behavior.
  No builder-specific workspace migration is required.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.8.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.8.0/apps/agents/builder/README.md)

## Commander spawner agent v0.8.0

- Align Commander module requirements with v0.8.0.
- Target a deployed spawner through the task assignment's hostname selector;
  `spawn-agent` input and package/checkpoint formats are unchanged.
- The controller preserves that filter across same-action batch children.
  Existing `count` limits remain 1–100, with a default of one.

Use `SERVER_HOSTNAME` when a spawner needs a stable advertised runtime name.
Children continue reporting their own hostname; this override is not forwarded.
Review each saved launch result and registered readiness before using the new
capability. Stop requests address agents through the controller, independently
of deployment-resource cleanup.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.8.0`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.8.0/apps/agents/spawner/README.md)

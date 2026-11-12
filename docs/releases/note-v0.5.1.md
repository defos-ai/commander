# Release notes v0.5.1

## Commander v0.5.1

Continue work in an active operation after a scan, a closed branch or submission
of a completion draft for operator review.

- Add optional `session_task_id` to MCP `assign_task` and HTTP task assignment.
  Reference a finished session-aware task in the same operation to reuse its
  retained resource and exact owner, including after its continuation has resolved.
  The new task is independent; the earlier decision and action input stay unchanged.
- Preserve session admission checks when reusing work: reject unknown or
  cross-operation task references, uncertain execution outcomes, blocking questions
  and closed resources. Unavailable owners never cause a session to move to another
  agent.
- Clarify MCP guidance that operator completion policy governs finalization,
  while active operations can continue accepting requested work. Clear requests
  can start without redundant confirmation; explicit approval requirements still
  apply. A completion draft does not grant model completion authority.
- Guide capability installation through a separate operation while the original
  mission waits. Installation completion alone does not establish readiness;
  registered capabilities and the original preflight must become ready. Queued
  work then resumes automatically without another assignment.
- Add public HTTP/MCP coverage for session reuse after branch closure and
  controller restart, and for capability registration resuming the original task.

### Upgrading from v0.5.0

- Update the controller, API and MCP together before using `session_task_id`, then
  reconnect MCP clients so they load the updated guidance and tool schemas.
- Retain saved state and the original encryption keys. This patch needs no
  saved-data migration or reset; existing operations and session bindings remain
  usable.
- Agent action inputs and the existing `open`/`use` contract do not change.
  Browser, Cybersec and other standalone agents publish separately; this patch
  does not require adding session IDs to their inputs.

[Session reuse](https://github.com/defos-ai/commander/blob/v0.5.1/docs/features/sessions.md#open-and-use) ·
[Installing missing capabilities](https://github.com/defos-ai/commander/blob/v0.5.1/docs/features/operations.md#install-missing-capabilities) ·
[Full changelog](https://github.com/defos-ai/commander/compare/v0.5.0...v0.5.1)

## Commander builder agent v0.5.1

- Align module requirements with Commander v0.5.1.
- With the matching controller and adapters, later workspace actions can reuse a
  finished task's session through assignment metadata after its original branch
  closes. Builder action inputs and the controller-owned `open`/`use` workspace
  contract remain unchanged.
- Retain existing workspaces, session state, host tools and managed credentials
  when upgrading.

Package: `github.com/defos-ai/commander/apps/agents/builder@v0.5.1`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/builder/v0.5.1/apps/agents/builder/README.md)

## Commander spawner agent v0.5.1

- Align module requirements with Commander v0.5.1. Native, Docker and Kubernetes
  launch behavior, source approval and the `spawn-agent` input contract remain
  unchanged.
- Run installation in a separate operation when another mission is blocked on
  missing capabilities. After installation, recheck the registered actions and
  the original mission's preflight; queued work resumes when its requirements
  become ready.

Package: `github.com/defos-ai/commander/apps/agents/spawner@v0.5.1`

[Usage and configuration](https://github.com/defos-ai/commander/blob/apps/agents/spawner/v0.5.1/apps/agents/spawner/README.md)

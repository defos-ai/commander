# Agent-owned runtime sessions

A runtime session is a resource that outlives an action and its operation. Its
reference is `{id, owner_agent_id, scope_id}`: canonical session/scope UUIDs and
the exact process's agent ID. The agent reports snapshots through its session
provider; Commander persists them in inventory and projects lease loss as `lost`.
A shell implementation lives in the external agent, not in the framework.

Actions declare `session_mode: create|use`. Their object input carries
`session_scope` and, for use, `session_id`. The controller treats owner routing as
an eligibility restriction: only the owning agent may run a use action or an
explicit create-action reuse. A stale, stopped, unknown, or unverified session
never falls back to another agent. A new create request is rejected if the scope
has a live reservation, and queued/running creation reserves the scope until its
first snapshot arrives. Explicitly close before replacing.

This reservation also applies to automatic follow-up proposals. Accepted session
results reserve their scope until inventory publishes that exact reference,
including across controller restart after the operation has ended. An interrupted
creation without a result remains uncertain until recovery obtains its outcome.
The contract cannot associate that task with an arbitrary closed session in the
same scope; closing an older session is not evidence that the pending creation
was stopped.

Mission and action input schemas may publish `x-commander-session-mode` with the
same values. A trusted chat host injects its chat UUID as `session_scope`, verifies
the session binding, and supplies the existing session ID for a use action. This
annotation is public client metadata; it never grants agent permissions.

MCP exposes:

- `list_runtime_sessions({scope_id})`: inventory snapshots for that scope.
- `control_runtime_session({id, owner_agent_id, scope_id, method})`: `inspect`,
  `verify`, or `close`, routed through the controller to that exact owner.

Control messages use `agents.<id>.controls`, separate from the serialized action
inbox. They remain available while an action is working. Verification must not
inject into busy command input. Providers return an error when a probe cannot
run; close stops the transport and reports its result. Controllers do not
interpret shell bytes, create replacement sessions, or replay commands.

`Session` includes `kind`, `label`, `state`, `busy`, revision/timestamps, optional
endpoint/peer/host/user, and reason. States are `starting`, `listening`, `verifying`,
`connected`, `unverified`, `lost`, `closed`, `failed`. Action outputs can include
the same typed `session` field. HTTP agent/action/result projections expose these
fields through explicit API mappings. Snapshots are independently cloned.

One active controller per database remains the supported coordination model.
Sessions are process-owned and cannot migrate after a restart. An unreachable
owner cannot confirm closure; clients retain that uncertainty. Session snapshots
expire with the existing agent inventory retention policy, so chat hosts must
persist their binding and refuse silent replacement if that record disappears.

`go test ./apps/adapters/api/tests/e2e -run '^TestE2E'` exercises automatic
creation reservations with real API/controller/worker processes. Cross-repository
shell E2Es live in the external agent repository; its `scripts/test-remote-sessions.sh`
uses caller-provided compatible Commander binaries to test public HTTP/MCP behavior.

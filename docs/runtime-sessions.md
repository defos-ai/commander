# Runtime sessions

Use a runtime session when an agent-owned resource must survive across actions or
operations. Declare `session_mode: create|use` on the action definition. Supply
`session_scope` in its input; `use` also needs `session_id`. Scope and session IDs
are canonical UUIDs; the owner ID identifies one exact agent process.
A create action with a session ID reuses that resource, even during setup; ordinary
use requires a connected resource.

## Provider contract

Implement `sessions.Provider` on the agent host and read the framework binding:

```go
binding, ok := action.SessionFromContext(ctx)
if !ok {
    return action.Result{}, errors.New("framework session binding required")
}
// Pass binding.Reference and binding.Creation to your resource adapter.
```

Implement `Prepare`, `Snapshot`, `Changes` and `Control` using the
[provider invariants](../framework/sessions/AGENTS.md#provider-contract). In
particular, close must prevent a delayed start from recreating the same resource.
Update external providers together with Commander; shell transport code lives in
its agent repository.

## Queries and controls

Use authenticated HTTP:

- `GET /api/v1/sessions?scope_id=<UUID>` lists current and retained sessions.
- `POST /api/v1/sessions/control` accepts
  `{id, owner_agent_id, scope_id, method, request_id?}`.

MCP offers `list_runtime_sessions({scope_id})` and `control_runtime_session`
with the same fields. Methods are `inspect`, `verify`, `close` and `abandon`.
Reuse a stable UUID `request_id` when reconciling a control. After a timeout,
inspect the saved session before retrying; a timeout does not prove closure.

Completion and cancellation leave resources alive. Close to release a scope.
Abandon only when accepting an uncertain old resource so a fresh operation can
replace it. Existing tasks and same-agent affinity retain their original owner.

Inspect `session_intent` and `preflight.tasks` for placement blockers.
`preflight.ready` alone does not mean every session is usable.

## E2E checks

From the repository root:

```sh
go test -race -count=1 -timeout=6m ./apps/adapters/api/tests/e2e -run '^TestE2E'
```

See [provider test setup](../framework/sessions/AGENTS.md#e2e-checks) for isolated
MongoDB/RabbitMQ coverage and [recovery contracts](../framework/sessions/AGENTS.md#checkpoints-and-recovery)
when changing lifecycle behavior. One active controller per database is supported.

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

Use [Admin session commands](../apps/admin/README.md#sessions), also available
through Playground as `/admin sessions`.

Use authenticated HTTP:

- `GET /api/v1/sessions` lists current and retained sessions. Filter by
  `scope_id=<UUID>`, `owner_agent_id=<agent-ID>`, or both.
- `GET /api/v1/sessions/<session-ID>` returns its saved scope, owner and state.
- `POST /api/v1/sessions/control` accepts
  `{id, owner_agent_id, scope_id, method, request_id?}`.

Call MCP `list_runtime_sessions({})` to discover sessions across all scopes;
pass optional `scope_id` and/or `owner_agent_id` filters to narrow the results.
The `sessions` array includes references, connection metadata, state and busy
status. Lists are ordered by scope ID, then session ID, and include closed or
abandoned history. An empty result is `[]`. Check state, disposition and action
readiness before using a session; listing alone does not guarantee readiness.

MCP also offers `control_runtime_session` with the control fields above.
Methods are `inspect`, `verify`, `close` and `abandon`.
Reuse a stable UUID `request_id` when reconciling a control. After a timeout,
inspect the saved session before retrying; a timeout does not prove closure.

Completion and cancellation leave resources alive. Close to release a scope.
Abandon only when accepting an uncertain old resource so a fresh operation can
replace it. Existing tasks and same-agent affinity retain their original owner.

Inspect `session_intent` and `preflight.tasks` for placement blockers.
`preflight.ready` alone does not mean every session is usable.

## E2E checks

The scenarios exercise public HTTP/MCP behavior through real hosts, including
MCP discovery across scopes, owner filters, validation and retained session history.
Run from the repository root:

```sh
go test -race -count=1 -timeout=6m ./apps/adapters/api/tests/e2e -run '^TestE2E'
```

See [provider test setup](../framework/sessions/AGENTS.md#e2e-checks) for isolated
MongoDB/RabbitMQ coverage and [recovery contracts](../framework/sessions/AGENTS.md#checkpoints-and-recovery)
when changing lifecycle behavior. One active controller per database is supported.

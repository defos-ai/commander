# Runtime sessions

Declare `session_mode: create|use` on an action's `actions.Definition` (including
`NewFunc` metadata). Its input supplies `session_scope`; use also requires
`session_id`. A create action with an explicit session ID means **reuse that
resource**, including while it is listening or unverified. Ordinary use requires
an observed connected resource. All IDs are canonical UUIDs except the exact
process owner's typed agent ID.

Commander saves the accepted intent with each task, reserves the scope, selects
an eligible owner and supplies an immutable binding to the action:

```go
binding, ok := action.SessionFromContext(ctx)
if !ok {
    return action.Result{}, errors.New("framework session binding required")
}
// Use binding.Reference and binding.Creation with your resource adapter.
// Never resolve whichever session happens to be current at execution time.
```

The controller resolves metadata at acceptance from advertised action contracts,
saved mission definitions and the learned catalog. Missing/conflicting contracts
reject direct requests; automatic proposals retain an explicit admission error
without losing the parent's result. Accepted intent is never reinterpreted after
a registration changes. Metadata supplies no readiness or execution permission.

## Ownership and replacement

The framework's durable session record owns reservation and lifecycle. The agent's
provider owns the resource. Task completion, operation completion, cancellation,
a failed action and inventory expiry do not prove that a resource was closed.
A new process cannot inherit an old process's session.

Each scope has one current reservation and retained historical identities.
`disposition` is `reserved`, `active`, `closing` or `released`; `state` describes
resource observation and reachability. Lease loss projects `lost` without
releasing the scope. Historical records survive inventory expiry and restart.

Close confirms the exact creation claim before releasing the scope. A failed or
timed-out close stays `closing` and is retried with the saved request identity.
Explicit abandonment of a lost/failed resource releases the scope while retaining
`state: lost`, `release_reason: abandoned` and its old identity. Best-effort
revocation is sent to that exact owner if it returns. Abandonment does not prove
closure: an unreachable owner may still run previously delivered work.

**Abandonment preserves existing operation affinity.** If operation O uses S on
A, O stays bound to A after abandoning S. A fresh operation can create S2 on B;
trying to use S2 from O reports a placement conflict. No queued use silently
switches to a replacement resource. Before first dispatch, accepted session
constraints also restrict earlier actions in the same `same-agent` group.
Tasks outside the group can use independent owners.

## Provider contract

Implement `sessions.Provider` on the agent host:

- `Prepare(ctx, claim)` registers the framework-issued creation identity before
  action validation, without opening a resource. Identical claims are idempotent.
- `Snapshot()` returns quick, independent, concurrency-safe observations.
- `Changes()` coalesces notifications that cause a full snapshot publication.
- `Control(ctx, request)` verifies or closes an exact reference. Update the
  snapshot before returning so the correlated owner announcement carries the
  receipt. A close before preparation must retain a closed tombstone for the
  validated creation claim and expose its closed observation.

Serialize resource Start and Close per claim. Close must block future starts,
resolve or stop a start in progress, and confirm only when that claim cannot
create or retain a live resource. If that cannot be established within the
control deadline, return an error. Keep tombstones for the process lifetime;
Prepare and explicit reuse must never reset them. This is the adapter's obligation
for real transports; Commander does not interpret shell bytes or open resources.

Controls use `agents.<id>.controls`, independent of the serialized action inbox.
They remain available during a long action. Verification must not inject into
busy command input. Established resources use the agent-instance lifetime, while
the action context bounds setup waits and command work.

Existing session providers need the new `Prepare` method and must use the binding
from context instead of allocating their own IDs. Update external agent packages
together with the Commander binaries; the external shell adapter is maintained
outside this repository.

## Queries and controls

HTTP (authenticated):

- `GET /api/v1/runtime-sessions?scope_id=<UUID>` returns current and retained views.
- `POST /api/v1/runtime-sessions/control` accepts
  `{id, owner_agent_id, scope_id, method, request_id?}`.

MCP exposes `list_runtime_sessions({scope_id})` and
`control_runtime_session` with the same control fields. Methods are `inspect`,
`verify`, `close` and `abandon`. Use a stable UUID `request_id` to reconcile a
control. Inspect is read-only. After an uncertain response, read the durable
view; a broker acknowledgement or request timeout is not a closure receipt.

Task responses include `session_intent`. `preflight.tasks` exposes the task's
exact owner, placement blocker and owner-specific missing requirements. The
operation-wide capability gate and per-task placement are separate assessments:
`preflight.ready` alone does not promise that every task or resource is usable.

A trusted chat host can inject its chat UUID as the scope. Input schema annotation
`x-commander-session-mode` remains client metadata; it grants no permissions.

## Checkpoints and recovery

The session binding is already in the saved execution command. Use
`action.SaveCheckpoint` for bounded execution facts such as
`session_setup_intent` before an effect and `session_setup_observed` afterward.
Identical saves are idempotent; changing a saved value is rejected. Another
execution cannot load those facts, and completion prevents adding new names.
Avoid secrets and raw command/output contents.

Checkpoints neither own session lifecycle nor authorize rerunning an effect.
Close and abandonment persist independently after the creating execution ends.
Startup reconciles saved intent, dispatch, owner binding and execution claims
before dispatch. Unknown outcomes retain the original owner and command; released
bindings allow observation of old outcomes without republishing old work.

One active controller per database is supported. FS stores scope aggregates in
`runtime-sessions.json`; Mongo stores one aggregate per scope in `runtime_sessions`
using transactions. There is no cross-store transaction or automatic history
pruning. Retained histories count toward backend storage limits (including
Mongo's document limit); archival must preserve release and identity evidence.

See the [architecture and recovery protocol](runtime-session-lifecycle-design.md)
and [domain glossary](../GLOSSARY.md).

## E2E checks

Run from the repository root; these tests launch real hosts and a resource fixture
and assert public HTTP outcomes. They create no unit tests.

```sh
go test -count=1 -timeout=6m ./apps/adapters/api/tests/e2e -run '^TestE2E'
```

For provider coverage, supply an isolated Mongo replica set and disposable
RabbitMQ broker outside the test process. Each scenario substitutes a fresh DB
name for the literal `{database}` placeholder. Never point the bus at a live
application deployment. Stop disposable services after the run.

```sh
COMMANDER_SESSION_E2E_DATABASE_URL='mongodb://127.0.0.1:27017/{database}?replicaSet=sessions&directConnection=true' \
COMMANDER_SESSION_E2E_BUS_URL='amqp://guest:guest@127.0.0.1:5672/' \
  go test -count=1 -timeout=6m ./apps/adapters/api/tests/e2e -run '^TestE2ESession'
```

Coverage includes automatic competing creations and restart, cross-operation use,
owner loss/abandonment without affinity rebinding, group lookahead and independent
owners, owner-specific secrets, queued cancellation, close during creation,
failed-close reconciliation, reuse before verification, checkpoint immutability
and isolation, and intent accepted before executor registration. Real shell
transport behavior belongs to the external agent's E2E suite.

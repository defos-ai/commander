# Working on runtime sessions

Read this guide when changing session admission, placement, providers, controls
or recovery, including callers outside this directory. For transition ordering
and failure cases, read the [lifecycle design](../../docs/runtime-session-lifecycle-design.md).

- [admission.go](admission.go) normalizes intent; [observations.go](observations.go)
  reconciles operation/execution evidence; [controls.go](controls.go) handles
  lifecycle controls. The domain owns transitions; stores own atomicity.
- Live and mission contracts take precedence over learned catalog metadata.
  Resolve from known contracts first; a transient catalog lookup failure must
  remain retryable, never become a permanent admission error. Freeze accepted
  intent despite later registration changes. Metadata grants no readiness.
- Operation persistence determines acceptance. Reconcile session writes afterward
  and retry failures, including for terminal operations. Gate dispatch on completed
  reconciliation; preserve uncertain operation writes as uncertain.
- Completing/cancelling work or expiring inventory does not release a session.
  Close confirms the exact creation claim. Failed close stays pending with its
  saved request ID; abandonment retains identity/history and best-effort revocation.
- Replacement never rebinds queued use or existing operation affinity. Before the
  first dispatch, session constraints restrict earlier actions in the same group;
  tasks outside it may use other owners.
- Keep lifecycle disposition (`reserved`, `active`, `closing`, `released`) separate
  from resource state. Lease loss projects `lost` while retaining the reservation.
- Session discovery uses `sessions.QueryStore.Find` with intersecting session,
  scope and exact-owner criteria. Providers filter and order independent records
  by scope ID, then session ID; `queries.Sessions` only projects owner freshness.
  Retain released identities in discovery. ID lookup rejects ambiguous matches
  rather than choosing a scope. Reads never change lifecycle evidence.
- Subscribe to the independent control inbox before advertising agent readiness.
  Preserve correlated receipts, retained history, owner-specific requirements and
  meaningful per-task placement across HTTP/MCP mappings. Ordinary unconstrained
  queue changes must not create preflight events or version bumps.

## Terminology

- **Runtime session**: a resource retained across actions/operations by one exact
  running agent instance (the **owner**); an agent type cannot inherit ownership.
- **Scope**: the context controlling replacement, sometimes provided by a chat;
  its **reservation** blocks competing creation while the outcome is unresolved.
- **Intent**: a saved request to create, reuse or use a session. Reuse can continue
  establishment; use requires connected state. Intent supplies no readiness.
- **Binding**: dispatched work's immutable session identity and exact owner.
- **Abandonment**: explicitly releasing a lost/failed session for replacement
  while preserving uncertain history; it is not proof of closure.
- **Same-agent affinity**: an operation's grouped actions share the first
  dispatched owner; it grants neither ordering nor session lifetime.
- **Execution checkpoint**: an immutable execution fact, not session state or
  permission to repeat an effect or transfer ownership.

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

Keep external providers aligned with the binding and Prepare contract; real shell
transport behavior belongs to the external agent repository.

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

## E2E checks

Run from the repository root; these tests launch real hosts and a resource fixture
and assert public HTTP outcomes. Use only E2E checks for session changes.

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

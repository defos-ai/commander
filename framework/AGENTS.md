# Working on the framework

## Boundaries and entry points

- Keep this module independent of applications and providers. Inject interfaces;
  concrete clients and provider construction belong in app factories.
- [`host/credentials.go`](host/credentials.go) owns the shared username/password
  type, validation and secure password generation. Environment names, defaults,
  startup policy and credential display belong to applications.
- Start with [`agent`](agent/) for execution and discovery,
  [`controller`](controller/) for inventory and dispatch, and
  [`controller/runner`](controller/runner/) for operation scheduling and recovery.
- Keep entities and store contracts in [`domain`](domain/), using singular files
  and types in plural packages, such as `agents.Agent` and `agents.AgentStore`.
  Transport commands belong in [`comms/clients`](comms/clients/).
- Preserve Commander → Controller → Agent control flow: adapters query saved
  state and send operation commands; only the controller selects agent instances.
- Mission definitions own automation. Actions may propose schema-valid next
  actions, but must never dispatch them or contact a controller themselves.

## Runtime and persistence invariants

- Use existing controller and runner owner loops for runtime mutation. Update
  persistence, consumers and recovery together when changing lifecycle behavior.
- Only one active controller per database is supported. Registration and selector
  capacity do not establish distributed ownership or global capacity.
- Apply operation mutations atomically to the latest saved record through
  [`OperationStore`](domain/operations/operation_store.go). Persist tasks and
  events separately; preserve independent copies at store boundaries.
- Storage locking, transactions and storage revisions belong in providers.
  Continuation versions are explicit business revisions that reject stale
  decisions, not storage revision numbers.
- Reconcile uncertain writes before dispatching more work. Dispatched work stays
  bound to its recorded agent and command; never reassign an unknown outcome.
  `RecoverAction` only observes work; it must not execute or republish commands.
- Preserve immutable execution outcomes and reports. Save reports before
  completing operations. Agents upload bytes through `ArtifactFileStore`;
  controllers accept metadata through `ArtifactStore`. Bytes alone are not evidence.
- Keep [`AgentSelector`](controller/runner/agent_selector.go) limited to choosing
  eligible candidates. The runner owns readiness, permission and capacity checks
  and dispatch persistence. Selection is serial, must honor cancellation and
  return promptly; an empty choice defers work.
- Keep registration ready only after recovery and consumers start. During
  shutdown, await runner and consumer termination before closing registration.

## Agent extension contracts

- Each process has a fixed agent type and fresh ID. Each agent owns fresh action
  instances because discovery state is mutable; propagate constructor errors.
- Discover executables and configuration through [`Environment`](agent/environment/environment.go).
  Missing requirements make only the affected action unavailable. Advertise
  secret names, never values, and recheck configuration during execution.
- Grant permissions explicitly and preserve schema validation of action input,
  output and proposed next actions. Public snapshots must remain independent.

## Validation

Run from `framework/` with the workspace disabled to check module isolation:

```sh
GOWORK=off go test -race -count=1 -timeout=3m ./...
GOWORK=off go vet ./...
```

Shared contract changes also need affected provider and app checks. Use the
[testing guide](tests/README.md) for service-backed suites and
[test-writing guidance](tests/AGENTS.md) for fixtures and failure scenarios.

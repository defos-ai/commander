# Working on domain models

Read the [framework guide](../AGENTS.md) and use the
[model reference](README.md) to find a model's purpose and consumers. This guide
applies to all domain packages below this directory.

## Ownership and placement

- Keep entities, pure validation/state transitions and store contracts here.
  Runtime orchestration belongs in `framework/agent`, `framework/controller`,
  `framework/sessions` or the relevant service. Concrete providers own storage
  revisions, locking and transactions; hosts select them in factories.
- Typed wire payloads belong in [comms/clients](../comms/clients/), and independent
  public HTTP DTOs belong in the API app. Adding a field requires reviewing its
  stored representation, runtime consumers and adapter projections together.
- Choose a package by the state it owns and its lifetime, rather than the caller
  that first produces it. A model is not automatically an execution record
  because an action reads or creates it. Use the shared model for both question
  entry points; do not introduce parallel operator/model question entities.
- Keep [README.md](README.md) current when adding, removing or changing a model's
  responsibility or consumers. Group supporting types with their owning model;
  leave field inventories and enum values in source.

## Preserve model distinctions

- Action calls, execution logs and messages expose `OperationID` and `TaskID`
  directly. Both may be empty for work outside an operation; otherwise both must
  be canonical UUIDs. Compare both when matching a dispatch, reply or delivery.
- [Action definitions](actions/definition.go) describe desired/static contracts.
  [Actions](actions/action.go) add observed readiness; a definition does not
  register an executor. [NextAction](actions/next_action.go) is a proposal whose
  admission and dispatch remain controller responsibilities.
- [AgentSnapshot](agents/agent_snapshot.go) is advertised state;
  [Agent](agents/agent.go) adds received observation and lease data. Keep agent
  type separate from exact process identity. Freshness is a read-time assessment,
  not proof that external work stopped.
- [Missions](missions/mission.go) are reusable definitions saved into each
  [operation](operations/operation.go). Action coverage does not automatically
  sequence tasks or act as an execution allowlist.
- Mission policy types live in [missions/policies](missions/policies/), which
  stays independent of its parent package. Completion, continuation
  and sharing are grouped under `MissionPolicies`.
- [`CompletionPolicy`](missions/policies/completion_policy.go) is optional under
  `MissionPolicies.Completion`. Omission/null preserves the model default; an
  explicit empty policy is invalid. Clone its pointer independently with the
  policy container. `Mission.CompletionPolicy` reads the effective value without
  filling omitted fields. Reject root `completion` instead of dropping operator
  authority while reading an old definition.
- [`ContinuationPolicy`](missions/policies/continuation_policy.go) is optional under
  `MissionPolicies.Continuation` and controls task/group follow-up decisions.
  Its aggregation/model-review scopes are independent and may overlap. Clone each
  slice independently, even when inputs share a backing array. Aggregation covers
  saved direct children; failures and empty proposals still require model review.
  Aggregated children retain a stable `Task.BatchID` for each accepted group,
  independent of later continuation versions and child lists. Task membership
  and `task.batch.started`/`task.batch.finished` events persist within the operation;
  no separate batch store exists. Finish requires known outcomes for every
  member, including failed or cancelled tasks. Interrupted outcomes remain
  unknown even when continuation review is ready. Late results can close an
  earlier batch after another group starts or the operation becomes terminal.
  Continuation does not override completion or sharing settings. Reject removed
  root `rules` and nested `policies.placement` / `policies.session` fields, including null or empty values,
  rather than silently accepting definitions with discarded constraints.
- Operation transitions own tasks, questions, continuations and events together.
  Use [OperationStore](operations/operation_store.go) mutation contracts rather
  than rebuilding a saved operation from a stale caller snapshot.
  Continuation evaluation stays pure in
  [continuation_evaluation.go](operations/continuation_evaluation.go), without a
  replaceable rule-applier interface. Commit effects through the existing atomic
  operation transitions.
  Completion uses the saved mission policy: model submissions either complete
  immediately or retain completion draft for operator review. Completion draft is
  operation state, not a question or a separate approval model; only explicit
  operator completion can finalize an operator-policy operation.
- [Questions](operations/question.go) support model-created operation/continuation
  decisions and action-originated execution waits. Preserve command/key
  idempotency and operator-first timing. Crossing `fallback_at` permits a model
  answer; it neither saves an answer nor expires the question. Execution
  questions must not mutate a running task's continuation. Accepted answer
  provenance identifies the submission path, not proof of a human's identity.
- [Task](operations/task.go) tracks orchestration;
  [Execution](executions/execution.go) retains a reserved command and accepted
  outcome. Reserve before publication, preserve the original envelope for replay
  and never infer that a missing result permits a new execution elsewhere.
  Terminal operation status and [settlement](operations/progress.go) differ:
  interrupted outcomes remain unknown, and late results can still be accepted.
- [Checkpoints](executions/checkpoint.go) are immutable execution facts;
  [logs](executions/log.go) are observations. Neither owns session lifecycle,
  extends task deadlines nor authorizes repeating an external effect. Keep
  append ordering and cursor positions inside the log-store provider.
- [Artifact files](artifacts/artifact_file.go) describe uploaded bytes;
  [artifacts](artifacts/artifact.go) contain accepted producer metadata.
  Preserve that upload/acceptance boundary. A [report](reports/report.go) records
  accepted final output and may document limitations.
- [Secret metadata and encrypted values](secrets/secret.go) have different access
  boundaries. A [Delivery](secrets/delivery.go) is a reserved encrypted storage
  snapshot; its wire delivery contains the decrypted requested value.
  [RequestRecord](secrets/request_record.go) commits request, reservation and
  acknowledgement together. Installation history does not prove current
  readiness. Never reuse questions/logs to transport or retain secret values.
  Request type defaults to reusable; transient requests retain exact execution
  IDs and a fixed deadline, without values. Replay identity includes effective
  type and execution scope. Reusable delivery/fulfillment cannot satisfy transient
  requests. Transient fulfillment retains only a delivery ID and receipt time,
  without a reusable secret identity, delivery snapshot, value or hash. Stores
  reserve delivery IDs permanently across both types; hosts validate transient
  receipts against the in-memory exchange before persisting them.
- Session [Scope and Record](sessions/lifecycle.go) own reservation and lifecycle;
  [Session](sessions/session.go) is an observed/query view. Preserve exact owner
  and original creation identity. Completing an operation does not close a
  session, abandonment does not prove closure, and close-before-start must fence
  delayed resource creation. Follow the [session guide](../sessions/AGENTS.md).
- [Controller registration](controllers/controller.go) describes runtime liveness.
  It does not grant distributed ownership or global scheduling capacity.
- Clone nested slices, maps, JSON bytes and pointers at snapshot boundaries.
  Domain validation establishes shape and invariants; hosts still own caller
  authorization, trusted-bus policy and secret encryption.

## Validation

For model or transition changes, run the framework checks from the repository root:

```sh
cd framework
GOWORK=off go test -race -count=1 -timeout=3m ./...
GOWORK=off go vet ./...
```

Check affected provider contracts and downstream hosts through their guides.
Exercise storage atomicity/uncertainty and API/MCP projection changes at their
actual boundaries. Follow the [shared test rules](../tests/AGENTS.md).
Documentation-only edits need local link/anchor and README/AGENTS pairing checks;
application tests are unnecessary for them.

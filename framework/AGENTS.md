# Working on the framework

## Boundaries and entry points

- Keep this module independent of applications and providers. Inject interfaces;
  concrete clients and provider construction belong in app factories.
- [`host/credentials.go`](host/credentials.go) owns the shared `Credentials`
  validation contract, `BasicCredentials`, `BearerCredentials` and secure secret
  generation. Environment names, defaults, startup policy and credential display
  belong to applications.
- Start with [`agent`](agent/) for execution and discovery,
  [`controller`](controller/) for inventory and dispatch, and
  [`controller/runner`](controller/runner/) for operation scheduling and recovery.
- Keep entities and store contracts in [`domain`](domain/), using singular files
  and types in plural packages, such as `agents.Agent` and `agents.AgentStore`.
  Transport commands belong in [`comms/clients`](comms/clients/).
- [`secrets`](domain/secrets/) separates public metadata from encrypted storage
  records. Keys use the registered agent type and a secret name. Secret versions
  identify value/validity changes, not provider revisions; recreated keys get fresh IDs.
  Requests identify the recipient agent and secret key; operation/task waits use
  `MissingRequirements` separately. Request fulfillment
  records a specific delivery acknowledged by its recipient, never mere availability.
  Historical fulfillment does not establish current readiness. Domain validation
  proves neither encryption nor authorization; both belong above the stores.
- [`secrets.RequestStore`](domain/secrets/request_store.go) persists a request,
  its one immutable encrypted delivery and installation acknowledgement together.
  Reserve before publishing; retries reuse the saved ID, timestamps and ciphertext.
  New versions use fresh requests. Keep exact replays idempotent and preserve
  terminal records. Fulfillment matches saved delivery evidence, not a separate
  SecretStore's latest value or a live agent's readiness; hosts own those checks.
  Delivery history retains encrypted copies even after deleting the primary secret.
- [`crypto.SecretCipher`](crypto/) encrypts records using an explicitly configured
  key. Apps load `SECRETS_ENCRYPTION_KEY` and inject the cipher; providers receive
  ciphertext only. Preserve all authenticated metadata, including nanosecond
  timestamps, across persistence. Encryption alone does not establish freshness.
- [`secrets.Service`](secrets/) encrypts before storing values and returns metadata
  only. It owns creation/version timestamps, requires ID/version for mutations,
  and preserves uncertain outcomes without automatic retries. Hosts authorize
  calls; saving a value does not deliver it or fulfill a request.
- Preserve Commander → Controller → Agent control flow: adapters query saved
  state and send operation commands; only the controller selects agent instances.
- Mission definitions own rules. Actions may propose schema-valid next
  actions, but must never dispatch them or contact a controller themselves.
- [`missions.MissionStore.Create`](domain/missions/mission_store.go) validates and
  atomically reserves an exact mission name; duplicates return `ErrMissionExists`
  even when the definition is identical. Keep `Upsert` for host-managed imports.
  Mission names cannot be `.` or `..`; clients must be able to address catalog
  entries safely.
  Providers own uniqueness and locks. Reconcile write errors by reading the
  catalog before retrying; saved operations retain their mission snapshots.
- Mission `required_actions` and step requirements participate in preflight;
  `optional_actions` describes expected capabilities and adds coverage only while
  its tasks are queued. Remove that extra coverage after dispatch/completion;
  normal execution checks still apply. Neither list restricts which actions may
  execute. Validate unique nonblank names and reject required/optional overlap;
  clone both lists at snapshot boundaries. Normal rules and readiness apply to optional work.
  Step requirements and `same-agent` groups still participate in preflight even
  if an action is optional; optionality does not override a placement constraint.
- [`missions.RuleSet`](domain/missions/rule.go) is saved in each operation's
  mission snapshot. [`operations.RuleApplier`](domain/operations/rule_applier.go)
  evaluates it without I/O; effects commit with the triggering transition.
  Valid proposals run automatically unless a matching rule requires model review;
  failures and empty proposals also require review. Aggregation uses the current
  decision's saved child IDs. Preserve parent grouping before evaluating children,
  each child's parentage, and fresh group membership on subsequent decisions.
- The `same-agent` mission rule binds its listed actions within one operation.
  [`Operation.AffinityAgentID`](domain/operations/affinity.go) derives the binding
  from retained task assignments; no separate binding record is stored. Failed,
  interrupted and cancelled dispatched tasks retain ownership. Enforce agreement
  atomically in `DispatchTask` and reject conflicting history during recovery.
  This is a placement constraint, not a continuation rule or execution ordering.
  Before binding, preflight limits grouped actions to agents advertising the
  entire group. After binding, assess only the saved owner's readiness. Include
  grouped actions even when omitted from `required_actions`; preserve the existing
  operation-wide preflight gate. Busy owners wait for normal scheduler capacity.
  Recheck the binding for each dispatch, including subsequent tasks in the same
  scheduler pass. Never fall back to another agent or rebind after an outage.
  Do not present secret requirements from agents excluded by affinity.
  `Operation.AssessAffinity` derives a read-only query view from the same history,
  retaining binding after completion or loss of registration. Keep it out of
  persisted operation fields; inconsistent history returns an explicit error,
  never an apparently unbound group.
- [`RuleSelector`](controller/runner/rule_selector.go) selects a complete rule set
  at operation start, after mission/input validation and before persistence. The
  default retains mission rules. Validate and clone selection output into the
  operation's mission snapshot; never reselect on recovery or per-task execution.
  Selection runs outside the owner loop, may overlap across starts, and must honor
  cancellation. Errors fail the start without falling back or saving an operation.

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
- [`Operation.AssessProgress`](domain/operations/progress.go) derives task counts,
  pending model decisions and unexpired questions using one supplied time. Only
  succeeded, failed and cancelled tasks are finished; interrupted outcomes remain
  unknown. A settled operation is terminal with every task finished. This is a
  saved-work view, not proof of agent idleness or permission to clean up state.
  Terminal operations retain history but expose no actionable decisions/questions.
  Reads never expire questions or change saved operation status.
- [`queries.Operations`](queries/operations.go) loads operation/task views for
  adapters, using one time per response for progress and live requirements.
  Inventory and parent-status read failures omit optional task observations;
  primary read failures and caller cancellation still fail the query. Compact
  listings skip inventory. Report reads verify operation ownership, and event
  waits use saved-work settlement with a final event reread. Keep wire formats,
  pagination validation and wait deadlines in adapters; query views are not stored.
- Preserve immutable execution outcomes and reports. Save reports before
  completing operations. Agents upload bytes through `ArtifactFileStore`;
  controllers accept metadata through `ArtifactStore`. Bytes alone are not evidence.
- [`ExecutionStore.SaveCheckpoint`](domain/executions/execution_store.go) records
  bounded, named JSON facts within a reserved execution. Use
  [`Execution.RecordCheckpoint`](domain/executions/checkpoint.go) for immutable
  transitions: identical retries confirm saved data, conflicting values fail,
  and completion prevents new names. Persist atomically with completion and other
  checkpoints; uncertain writes must be reconciled before proceeding. Keep
  snapshots independent and secrets/file contents out of checkpoints. Checkpoints
  do not authorize retry or transfer execution ownership.
- [`action.LoadCheckpoint` and `action.SaveCheckpoint`](agent/action/checkpoints.go)
  use an execution-scoped capability injected only during controller-dispatched
  work in `Agent.Serve`. Bind identities from the accepted command, overriding
  caller context. End waits when the caller, action or serving instance ends,
  including when the action supplies a detached context. Requests use controller
  events; replies use the agent event inbox so long-running actions cannot block
  them. Match reply/request IDs, command, name and work before accepting data.
  The controller owner loop verifies the saved dispatch claim and acknowledges
  saves only after persistence and readback. Retry the same immutable checkpoint
  request after an uncertain publication or lost reply, never the action itself.
- Keep [`AgentSelector`](controller/runner/agent_selector.go) limited to choosing
  eligible candidates. The runner owns affinity, readiness and capacity checks
  and dispatch persistence. Selection is serial, must honor cancellation and
  return promptly; an empty choice defers work.
- Operation preflight exposes missing requirements only for live, ready/working
  candidates, retaining their registered agent type. Inventory retains the last
  snapshot for offline candidates. Managed secrets use requirement kind `secret`
  and must be declared in the action's `RequiredSecrets`. Sensitive environment
  requirements and pending sync requests alone do not establish a secret wait.
  Queued work resumes through normal readiness refresh, never merely because a
  value was saved or a delivery acknowledged.
- Keep registration ready only after recovery and consumers start. During
  shutdown, await runner and consumer termination before closing registration.
  Hosts register both selectors' `Name()` values with the same implementations
  injected into the runner.

## Agent extension contracts

- [`agent/manifest`](agent/manifest/manifest.go) owns release manifest types,
  strict `Parse` and `Render` using the embedded [`agent.yaml.tmpl`](agent/manifest/agent.yaml.tmpl).
  Both APIs use the same validation; reject invalid supplied modes and require
  at least one. Render deterministically and quote strings to preserve YAML types.
  Release tooling supplies actual binary checksums and a digest-pinned image from
  the same tested revision, then writes and publishes the bytes. Keep host settings
  and secrets out of manifests.
  Optional `responsibilities` and `actions` describe published capabilities before
  installation. Actions contain only name and description, never runtime status
  or schemas. Accept omitted metadata in existing releases; reject explicitly
  empty lists, invalid text, duplicate names and unsafe YAML. Keep the 64 KiB
  document limit, 100-entry list bounds and 8 KiB text bounds. Preserve
  responsibility order and sort a cloned action list when rendering.
  See the [package contract](../docs/agent-packages.md#source-template).
  Cover the shared contract in [`tests/agent/manifest`](tests/agent/manifest/).
- [`agent/packages`](agent/packages/) owns GitHub package reference/selector
  parsing, strict discovery catalogs and bounded remote catalog loading. Selectors
  accept GitHub HTTPS URLs or canonical references, with omitted versions meaning
  `@stable`; exact persisted references remain strict. The official source is
  [`catalog/agents.yaml`](../catalog/agents.yaml); runtime readers fetch a configured
  remote source without requiring a checkout. Catalog entries require repository
  `visibility: public | private`; this describes access, not trust or image
  visibility. The shared catalog client remains anonymous. Catalog entries
  support discovery, not an enforced installation allowlist. The spawner owns
  version selection, manifest resolution and executable downloads, including
  scoped GitHub authentication. Its resolver selects `stable` from the repository's
  published root Latest release and maps module references to that version's
  module tag. The spawner runtime saves the exact pin before preparing a launch. The framework
  must not depend on the spawner. Cover catalog/reference behavior and the
  official catalog in [`tests/agent/packages`](tests/agent/packages/).
- Each process has a fixed agent type and fresh ID. Each agent owns fresh action
  instances because discovery state is mutable; propagate constructor errors.
- Hosts accepting a preassigned agent ID from outside accept only an eight-digit
  lowercase hex suffix. `id.AgentIDFromSuffix` combines it with the host's fixed
  type; persisted IDs and `AgentConfig.ID` remain complete typed runtime IDs.
- Discover executables and configuration through [`Environment`](agent/environment/environment.go).
  Missing requirements make only the affected action unavailable. Advertise
  secret names, never values, and recheck configuration during execution.
- `Action.RequiredSecrets` declares names even when discovery succeeds;
  `MissingRequirements` describes only current blockers. Requirement kind `secret`
  identifies managed secrets; the `Sensitive` flag on `configuration` identifies
  sensitive host configuration and does not imply a managed value. Resolve declarations
  against the registered agent type. Preserve declarations in snapshots, catalog
  projections and independent copies.
  Use per-agent actions for secret distribution; the global action catalog does
  not identify the recipient or scope. Gate declared names in agent snapshots and
  again before execution; missing secret configuration must not bypass the gate.
- Agent secret synchronization uses `AgentConfig.Secrets` and the existing event
  inbox, independently of long action commands. Keep values in memory and expose
  independent copies through `action.Secret(ctx, name)` only for the current
  action's declared names. Never use process-wide environment mutation. Reuse
  request/delivery/acknowledgement identities on retry; refresh with fresh requests
  and reject inactive or expired deliveries. Clear held values at shutdown.
  `action.RequestSecret(ctx, name)` lets a running action request a valid name
  on demand within its agent type. Keep that decision in action code; do not add
  input-condition rules to action metadata or controller preflight. Restrict the
  request capability to active execution in `Serve`, and end waits on caller or
  host cancellation. Wake waiters directly on installation, independently of
  rediscovery and the action gate. Stop refreshing unused dynamic requests;
  preserve installation acknowledgement retries and finite cached leases.
  Report active runtime waits through the existing work snapshot's
  `MissingRequirements`, using the same requirement shape as discovery. Keep
  counts local to each execution, independent of the shared secret delivery/cache.
  Clear evidence on installation, cancellation and execution end. API/MCP task
  reads use `operations.AssessTaskRequirements` to match live work to its saved
  assignment and deadline; this projection does not change task state, suspend
  timeouts, or authorize retries. Static action readiness remains unchanged.
- Controller secret synchronization runs in the existing owner loop with paired
  `SecretStore`/`SecretRequestStore`. Persist before publishing and check the live
  recipient's registered type and current stored version. Agents may request any
  valid name within that type; static declarations govern readiness, not access.
  The controller never decrypts. Keep trusted-bus and finite-lease revocation limits
  explicit; historical fulfillment does not authorize dispatch by itself.
- Preserve schema validation of action input, output and proposed next actions.
  Public snapshots must remain independent.
- `action.ExecutionFromContext` exposes the current command/work identity for
  executors that need to correlate external side effects. `Agent.work` supplies
  this context and overrides caller metadata; identity is not action input or an
  authorization grant. Executors still own durable side-effect reconciliation.

## Validation

Run from `framework/` with the workspace disabled to check module isolation:

```sh
GOWORK=off go test -race -count=1 -timeout=3m ./...
GOWORK=off go vet ./...
```

Shared contract changes also need affected provider and app checks. Use the
[testing guide](tests/README.md) for service-backed suites and
[test-writing guidance](tests/AGENTS.md) for fixtures and failure scenarios.

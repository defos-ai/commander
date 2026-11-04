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
  [`controller/workers/operation`](controller/workers/operation/) for operation scheduling
  and recovery.
- Keep entities and store contracts in [`domain`](domain/), using singular files
  and types in plural packages, such as `agents.Agent` and `agents.AgentStore`.
  Transport commands belong in [`comms/clients`](comms/clients/).
- [`secrets`](domain/secrets/) separates public metadata from encrypted storage
  records. Keys identify one shared value by name within a deployment.
  Secret versions identify value/validity changes, not provider revisions;
  recreated keys get fresh IDs.
  Requests identify the recipient agent and secret key. Omitted `type` means
  reusable; transient metadata requires canonical operation, task and command IDs.
  Type and execution IDs are immutable replay identity. Transient fulfillment
  retains only the delivery ID and receipt time; reusable delivery snapshots and
  installation evidence cannot be attached. Values remain outside transient records.
  Operation/task waits use `MissingRequirements` separately. Request fulfillment
  records a specific delivery acknowledged by its recipient, never mere availability.
  Historical fulfillment does not establish current readiness. Domain validation
  proves neither encryption nor authorization; both belong above the stores.
- [`secrets.RequestStore`](domain/secrets/request_store.go) persists a request,
  its reusable encrypted delivery and installation acknowledgement together.
  Reserve before publishing; retries reuse the saved ID, timestamps and ciphertext.
  New versions use fresh requests. Keep exact replays idempotent and preserve
  terminal records. Fulfillment matches saved delivery evidence, not a separate
  SecretStore's latest value or a live agent's readiness; hosts own those checks.
  Delivery history retains encrypted copies even after deleting the primary secret.
  `FulfillTransient` saves receipt metadata only after the controller verifies its
  held delivery. It never persists transient values, ciphertext or fingerprints.
- `ControllerConfig.SecretDecryptor` decrypts the reserved encrypted snapshot
  before publishing its metadata and requested value to the recipient. Inject
  the host's cipher; clear returned plaintext, sanitize decryptor errors and
  recheck deadlines/versions afterward. Missing decryptors cannot deliver values.
  Failed authentication leaves the encrypted reservation available for recovery.
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
- Mission definitions own policies. Actions may propose schema-valid next
  actions, but must never dispatch them or contact a controller themselves.
- [`missions.MissionStore.Create`](domain/missions/mission_store.go) validates and
  atomically reserves an exact mission name; duplicates return `ErrMissionExists`
  even when the definition is identical. Keep `Upsert` for host-managed imports.
  Mission names cannot be `.` or `..`; clients must be able to address catalog
  entries safely.
  Providers own uniqueness and locks. Reconcile write errors by reading the
  catalog before retrying; saved operations retain their mission snapshots.
- Mission `objectives` is an ordered, nonempty array of nonblank strings. Clone
  the array at snapshot boundaries and retain each entry through storage and
  transport.
- Optional mission [`policies.sharing`](domain/missions/policies/sharing_policy.go)
  declares action-to-action routes for results,
  artifacts and decisions within an operation. Each entry requires nonempty,
  unique `from_actions`, `to_actions` and supported `include` values. Action names
  need not appear in coverage lists, which are not allowlists. Absent/null `policies`
  and an absent/null/empty sharing list declare no routes. Empty policies are valid.
  Clone the policy container independently and deep-clone each nested slice into
  operation snapshots. Results, accepted artifact metadata and task-scoped
  answered questions can be forwarded to existing active tasks. Sharing does not
  start tasks or grant secret/storage access.
- Optional mission `policies.completion` stores a mission-wide
  [`CompletionPolicy`](domain/missions/policies/completion_policy.go) with
  required `decision_by: model|operator`. Omission/null retains a nil policy;
  an empty object is invalid. Clone its pointer independently inside policies in
  mission and operation snapshots. `Mission.CompletionPolicy` supplies the effective
  model default without changing the retained definition. Reject the old root
  `completion` field. Trusted adapters stamp required completion origin:
  HTTP API uses `operator`, MCP uses `model`; public bodies cannot choose it.
  Model/omitted policy completes immediately through the existing command.
  For operator policy, model completion saves one `Operation.CompletionDraft`
  after readiness/schema checks and returns awaiting operator approval. It
  creates no report, changes no status and resolves no continuations. Identical
  output replays preserve events/time; changed output replaces the saved field.
  Keep JSON bytes independent and preserve them across storage and recovery.
  Operator completion validates explicitly submitted output, creates the report,
  clears completion draft and saves `completed_by` atomically with final state.
  Stores recheck authority inside completion; completion draft never grants it.
  Saved completion draft does not freeze later work, so review current eligibility
  before accepting it. Retain it as history on failure/cancellation. This records
  the submission channel, not proof of a human's identity. There is no question
  timeout or model fallback for operator completion. Continuation policies and
  question behavior remain unchanged. Reconcile uncertain writes before another
  mutation; completion draft has no separate IDs, origin metadata or command.
- Optional mission `action_definitions` reuses `actions.Definition` for desired
  capabilities. Keep at most 100 unique trimmed names, each referenced by required,
  optional actions. Compile both explicit object schemas with
  the shared action resolver. Deep-clone definitions, schema bytes and secret names
  in mission/operation snapshots. Definitions do not register executors, grant
  secrets, alter live inventory or satisfy preflight readiness.
- Mission `required_actions` participate in preflight;
  `optional_actions` describes expected capabilities and adds coverage only while
  its tasks are queued. Remove that extra coverage after dispatch/completion;
  normal execution checks still apply. Neither list restricts which actions may
  execute. Validate unique nonblank names and reject required/optional overlap;
  clone both lists at snapshot boundaries. Normal policies and readiness apply to optional work.
- Optional [`policies.continuation`](domain/missions/policies/continuation_policy.go)
  is saved in each operation's mission snapshot. Its independent `aggregate_results`
  and `model_review` action lists may overlap; names are trimmed, unique and
  nonblank within each list without a coverage-list membership requirement.
  Omitted/null continuation, empty objects and nil/empty lists add no overrides.
  Deep-clone its pointer and both slices independently, even with shared input
  backing arrays. [`operations.EvaluateContinuation`](domain/operations/continuation_evaluation.go)
  evaluates snapshots without I/O or mutation; effects commit with the triggering
  transition. Valid proposals run automatically unless the policy requires model review;
  failures and empty proposals also require review. Aggregation uses the current
  decision's saved child IDs. Preserve parent grouping before evaluating children,
  each child's parentage, and fresh group membership on subsequent decisions.
- Session-aware tasks remain bound to the exact resource owner recorded by their
  accepted session intent. Stateless tasks and creation intents without an owner
  select ready eligible agents. Mission `policies.session` resolves managed resource
  bindings before accepting work; creator candidates must declare their consumers,
  without requiring those consumers to be ready. Follow the
  [session guide](sessions/AGENTS.md#mission-managed-bindings). Reject removed `policies.placement`
  and root `rules` fields instead of silently dropping constraints on reads.
- Operation startup validates the stored mission/input and clones the mission
  directly into its saved snapshot. Policy behavior belongs to that definition;
  hosts do not select or override policies. Recovery and task execution use the
  saved snapshot, independent of later mission edits.

## Runtime and persistence invariants

- Runtime-session changes must follow the [session guide](sessions/AGENTS.md),
  including when changing callers outside the session directory. The
  controller's `session.Worker` owns admission, binding, exact execution
  claiming and publication ordering. The operation worker consumes the shared
  `sessions.Coordinator` interface backed by that same instance. Persist normalized
  intent with automatic children.
  Preserve scope history across inventory expiry; abandonment releases only the
  scope, retaining saved session identities and ownership. Checkpoints remain
  execution facts.
  Use [session E2Es](../apps/adapters/api/tests/e2e/session_lifecycle_test.go) for
  public recovery, owner placement and lifecycle behavior.
- Once serving, failed heartbeat and uncorrelated session-state publications do
  not end the agent unless the bus is closed or its service is cancelled. Log
  failures without payloads; later full-state heartbeats restore observations
  after controller restart. Keep startup, correlated control replies and durable
  result-acceptance handling separate from these periodic observations.
- Controller owns worker lifetime, startup, shutdown and delegating facades.
  [NewController](controller/controller.go) owns `ControllerConfig`, defaults and
  validation, and constructs the host with admission and core lifetimes.
  [Assembly](controller/assembly.go) receives that host and config, then builds and
  returns the worker list without calling `Run` or starting workers.
  `NewController` starts the returned list; [worker subpackages](controller/workers/AGENTS.md)
  retain their logic and private state. The parent workers package owns their
  shared contract and lifecycle helper. Use coordination and operation worker
  owner loops for runtime mutation. Update persistence, consumers and recovery together when
  changing lifecycle behavior.
- Controller lifecycle startup joins command intake, the operation worker,
  coordination, events, sessions, secret reconciliation and sharing through one completion
  signal. Keep remote waits outside the owner loop. Each worker owns its blocking `Run(ctx)` and
  `Close(ctx)`; the controller retains the complete list before starting it.
  `Done` signals that public admission has stopped. Shared admission cancellation
  may begin command and operation worker draining together. Graceful shutdown joins
  command requests and subscription cleanup first, then the operation worker, while coordination
  and event handling remain available for accepted results. Then cancel the core
  lifetime and close its workers concurrently. Fatal core errors cancel that lifetime immediately and
  fence admission before pending callers wake. `Close` waits under its caller's
  context; cleanup continues independently after a timeout so retries await the
  same work and error. Hosts retain their providers until shutdown finishes.
- [`operation.New`](controller/workers/operation/worker.go) restores saved state
  synchronously without launching task execution or background observation.
  Controller assembly injects the same session worker, explicit sharing capability
  and a typed executor that uses coordination directly, so draining bypasses public
  admission. Assembly subscribes the command and cancellation inboxes after
  recovery; adapters submit through that owned worker. Do not construct another
  operation worker or command consumer beside the controller. Standalone hosts must start
  [`Worker.Run`](controller/workers/operation/lifecycle.go) before submitting mutations.
  `Run` is blocking and single-use: concurrent calls return `ErrAlreadyRunning`;
  calls after shutdown return `ErrClosed`. Constructor-context cancellation,
  Run-context cancellation and `Close` stop admission. The running loop joins
  accepted calls and retains successful late results before returning saved write
  errors. `Close` also works before `Run`, bounds its wait by the caller's context
  and may be retried after a timeout; it does not cancel saved operations.
- Only one active controller per database is supported. Registration and selector
  capacity do not establish distributed ownership or global capacity.
- Apply operation mutations atomically to the latest saved record through
  [`OperationStore`](domain/operations/operation_store.go). Persist tasks and
  events separately; preserve independent copies at store boundaries.
  Question answers save `answered_by` (`operator` or `model`) atomically with the
  answer and any linked continuation. Trusted adapters stamp this origin: HTTP
  API submissions use `operator`, MCP submissions use `model`. It records the
  submission channel; public answer bodies cannot choose it.
  Questions store one `fallback_at`, derived from `timeout_seconds` (120 seconds
  by default). Reject model answers before it inside the atomic mutation;
  operators can answer at any time. Passing this time leaves the question and
  its blocked continuation pending. Recovery retains the original fallback time;
  there is no question expiry or automatic answer.
- Storage locking, transactions and storage revisions belong in providers.
  Continuation versions are explicit business revisions that reject stale
  decisions, not storage revision numbers.
- Reconcile uncertain writes before dispatching more work. Dispatched work stays
  bound to its recorded agent and command; never reassign an unknown outcome.
  `RecoverAction` only observes work; it must not execute or republish commands.
- The operation worker's [bounded observations](controller/workers/operation/observations.go)
  reconcile interrupted executions while the controller remains running, including after an
  operation is completed, failed or cancelled. Only successful output or a typed
  `executions.CommandFailure` establishes a known outcome. Inconclusive checks
  preserve the original interruption, deadline, continuation and event history.
  Suspend observation and result writes for operations with uncertain state;
  retain terminal operation status and immutable reports when late evidence arrives.
- [`Operation.AssessProgress`](domain/operations/progress.go) derives task counts,
  pending model decisions and unanswered questions. Only
  succeeded, failed and cancelled tasks are finished; interrupted outcomes remain
  unknown. A settled operation is terminal with every task finished. This is a
  saved-work view, not proof of agent idleness or permission to clean up state.
  Terminal operations retain history but expose no actionable decisions/questions.
  Reads never resolve questions or change saved operation status.
- [`queries.Operations`](queries/operations.go) loads operation/task views for
  adapters, using one time per response for progress and live requirements.
  Inventory and parent-status read failures omit optional task observations;
  primary read failures and caller cancellation still fail the query. Compact
  listings skip inventory. Report reads verify operation ownership, and event
  waits use saved-work settlement with a final event reread. Keep wire formats,
  pagination validation and wait deadlines in adapters; query views are not stored.
  [Log queries](queries/logs.go) require a canonical operation ID, verify the saved
  parent and optional task membership, then delegate unchanged criteria to the
  execution store. Missing scope records retain their typed errors; logs need no
  live inventory or controller connection. Inject `Executions` only when enabling
  these reads; existing operation/task/report queries do not require it.
- Task, event and execution-log store pages use
  [`domain.ValidatePageLimit`](domain/pagination.go) to enforce `1..MaxPageSize`
  before storage I/O. Adapters supply defaults and validate their wire parameters.
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
- Execution logs use [`executions.Log`](domain/executions/log.go) for progress
  observations, separate from checkpoint facts and terminal outcomes. Messages
  preserve nonblank UTF-8 text up to 2048 bytes; keep secret values out of them.
  [`action.Log`](agent/action/log.go) accepts only the message through an injected
  `LogProvider`; public execution metadata does not grant that capability.
  [`ExecutionStore.AppendLog`](domain/executions/execution_store.go) retains each
  observation in separate storage from claims/checkpoints/results. Exact ID/content
  retries succeed; conflicting reuse fails.
  Write errors may follow a commit: read by ID or retry the identical observation.
  Stores validate the record shape; controllers must verify saved execution claims.
  [`ExecutionStore.ListLogs`](domain/executions/execution_store.go) follows task
  pagination: an empty log-ID cursor starts the first page; subsequent cursors
  must identify committed logs matching all operation/task/agent/action filters.
  Unknown or mismatched cursors return `ErrUnknownLog`. `LogQuery.Order` accepts
  empty/default `asc` or `desc` over committed append positions. Ascending pages
  start at the oldest matches and continue above the cursor; descending pages
  start at the newest matches and continue below it. Return up to the requested
  limit in that order. Adapters use the last returned ID for the next cursor and
  preserve it on empty pages. To follow new logs after a descending read, use
  ascending order from its first/newest returned ID.
  Append positions stay private to providers; producer timestamps and UUID order
  do not determine pagination. Store filters neither authorize access nor verify
  operation/task existence. Shared operation queries verify those saved scopes;
  adapters own access policy.
  `Agent.Serve` binds the logging capability to the accepted agent, command, action
  and work context. Standalone `Work` clears inherited providers. Caller, action
  and serving lifetimes bound publication, including detached caller contexts.
  A successful `Log` confirms bus publication only; there is no persistence receipt
  or automatic retry. Each call creates a new observation ID.
  The controller owner loop verifies the saved claim, appends and reads back the
  observation before ACK. Valid delayed logs remain acceptable after completion.
  Log-processing failures reject that delivery and allow other work to continue;
  receive and settlement failures retain their normal shutdown behavior. Logging
  never extends deadlines or changes operation state.
- [`action.RunWithProgress`](agent/action/progress.go) wraps one stage with fixed,
  non-sensitive labels and best-effort start/end messages plus liveness every 30
  seconds. Each publication has a one-second budget and no retry. Preserve the
  work's exact error, never cancel its context, and cancel/join the reporter on
  return, panic or caller cancellation. An unavailable log capability skips the
  reporter. Completion describes only that named stage, not acceptance of an
  artifact, a remote side effect or the entire operation.
- [`action.ReceiveContext`](agent/action/context.go) waits on an execution-scoped
  memory buffer fed by the independent agent event inbox; it sends no request.
  Bind it before announcing `AgentWorking`, match the exact recipient agent,
  command and work identity, and keep source and recipient in the same operation.
  Preserve saved source IDs and deep-copy result JSON, artifact metadata and
  answered questions. Receivers own deduplication and aggregation. The 32-update
  buffer fails with `ErrContextOverflow` rather than silently continuing with
  incomplete data; it never blocks the event consumer. Caller cancellation,
  execution/service end and command deadlines bound waits, including detached
  contexts. Standalone work clears inherited access. Discard buffered data when
  execution ends; delivery neither changes action outcomes nor extends deadlines.
  No publication records, replay or durable receiver memory exist.
- The [sharing worker](controller/workers/sharing/worker.go) receives stores, messaging and
  live-agent observation through injected dependencies. The controller owns its
  lifecycle; sharing must not import the parent package or mutate controller state.
  [Result forwarding](controller/workers/sharing/results.go) reads the saved operation
  policy, source execution and task dispatch after result/artifact acceptance.
  Union matching routes and verify current operation/task state, unfinished
  recipient claim and fresh observed work before exact-target delivery. Never
  forward action input, proposals, session mutations or file bytes. A bounded
  worker keeps forwarding I/O outside the owner loop; failures and queue overload
  are logged without changing accepted outcomes.
  Cancel and join it during shutdown. Recovery reads do not replay sharing, and
  saved source IDs let receivers deduplicate accepted event redeliveries.
- After a successful answer save, the operation worker notifies its explicitly
  injected sharing capability with only operation/question IDs. The sharing worker
  [rereads the saved decision](controller/workers/sharing/decisions.go)
  and matches its task to the original execution claim before routing. Preserve
  question IDs, content, timestamps and answer origin; multiple decisions can
  belong to one execution.
  Operation-wide questions are not shared. Pending, rejected or uncertain answers
  and ordinary reads/recovery never trigger forwarding. Delivery failures do not
  change an answer or its continuation; sharing grants no completion authority.
- Keep [`AgentSelector`](controller/workers/operation/agent_selector.go) limited to choosing
  eligible candidates. The operation worker owns session ownership, readiness,
  capacity checks and dispatch persistence. Selection is serial, must honor cancellation and
  return promptly; an empty choice defers work.
- Operation preflight exposes missing requirements only for live, ready/working
  candidates, retaining their registered agent type. Inventory retains the last
  snapshot for offline candidates. Managed secrets use requirement kind `secret`
  and must be declared in the action's `RequiredSecrets`. Sensitive environment
  requirements and pending sync requests alone do not establish a secret wait.
  Queued work resumes through normal readiness refresh, never merely because a
  value was saved or a delivery acknowledged.
- Keep registration ready only after recovery and consumers start. During
  shutdown, await operation worker and consumer termination before closing registration.
  Hosts register the agent selector's `Name()` using the same implementation
  injected into the operation worker.

## Agent extension contracts

- [`actions.Definition`](domain/actions/definition.go) owns static action metadata:
  name, description, input/output schemas, session mode and required secret names. Executor
  bases retain this static contract separately from readiness. `actions.Action`
  is the flat inventory snapshot, and
  `Action.Definition()` returns an independent static copy. Share metadata
  validation and cloning through `Definition`; keep live requirement checks in
  `Action.Validate`. Metadata validation requires JSON-object schemas; typed
  executors additionally compile their schemas and validate execution data.
  `actions.ResolveSchema` owns explicit object schema compilation, local reference,
  default and type validation. Executors use it through `action.ResolveSchema`;
  mission contracts use it directly, without importing the agent runtime.
  Constructors accept action metadata and may infer schemas later, so validate
  secret declarations separately before inference. Release `manifest.Action`
  remains a name/description summary.
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
  `manifest.Action.Validate` shares release-safe name and description constraints
  with callers preparing action contracts before a build.
  See the [package contract](../docs/agent-packages.md#source-template).
- [`agent/packages`](agent/packages/) owns GitHub package reference/selector
  parsing, strict source definitions and bounded remote source loading. Selectors
  accept GitHub HTTPS URLs or canonical references, with omitted versions meaning
  `@stable`; exact persisted references remain strict. The official source is
  [`sources/agents.yaml`](../sources/agents.yaml); MCP loads it anonymously for
  discovery. Source entries require repository
  `visibility: public | private`; this describes access, not trust or image
  visibility. The shared source client remains anonymous. Source entries
  support discovery, not an enforced installation allowlist. The spawner owns
  version selection, manifest resolution and executable downloads, including
  scoped GitHub authentication. Its resolver selects `stable` from the repository's
  published root Latest release and maps module references to that version's
  module tag. The spawner runtime saves the exact pin before preparing a launch. The framework
  must not depend on the spawner. Verify source/reference behavior through
  the public installation integration scenarios.
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
  sensitive host configuration and does not imply a managed value. Resolve
  declarations by shared secret name. Preserve declarations in snapshots, catalog
  projections and independent copies.
  Use per-agent actions for secret distribution; the global action catalog does
  not identify the recipient. Gate declared names in agent snapshots and
  again before execution; missing secret configuration must not bypass the gate.
- Agent secret synchronization uses `AgentConfig.Secrets` and the existing event
  inbox, independently of long action commands. An empty `SecretConfig` enables
  it without a decryptor or encryption key. Keep received values in memory and expose
  independent copies through `action.Secret(ctx, name)` only for the current
  action's declared names. Never use process-wide environment mutation. Reuse
  request/delivery/acknowledgement identities on retry; refresh with fresh requests
  and reject inactive or expired deliveries. Clear held values at shutdown.
  `action.RequestSecret(ctx, name)` lets a running action request a valid name
  on demand by its shared name. Keep that decision in action code; do not add
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
- `action.RequestSecret` accepts at most one
  [`SecretRequestOptions`](agent/action/secret_request_options.go); omitted type
  means `SecretReusable`. `RequestReusableSecret` and `RequestTransientSecret`
  select their type and delegate to it. Reject conflicting types and all transient
  defaults, including nonnil empty values. Options keep defaults out of JSON and
  formatted diagnostics. `SecretOptionsRequester` receives a cloned default that
  is cleared when the call returns; never clear caller-owned buffers. Preserve
  existing `SecretRequester` providers for reusable calls without defaults, and
  return `ErrSecretRequestUnsupported` when they cannot honor the options. The
  built-in runtime supports reusable and transient requests without defaults;
  default seeding remains unsupported. Transient requests bind to the accepted
  execution, use the original request TTL bounded by caller/command lifetimes,
  and never renew automatically. Each call has its own request and memory wait,
  even for the same name. Keep transient values out of the reusable agent cache.
  Clear owned bytes after handoff, cancellation, expiry and service shutdown;
  retain only bounded receipt/cancellation metadata for exact transport retries.
  Reject stale or mismatched replies before accepting bytes, including detached
  callers whose execution has ended. A delivery receipt proves arrival, never
  successful website submission; uncertain browser work needs explicit recovery.
- Controller secret synchronization has its own worker with paired
  `SecretStore`/`SecretRequestStore`. Persist before publishing and check the live
  recipient's registered type and current stored version. Agents may request any
  valid shared name; static declarations govern readiness, not access.
  Serialize event handling and periodic reconciliation through the coordination
  mailbox so inventory observations and deliveries retain one owner.
  Only the controller decrypts for delivery. Keep trusted-bus and finite-lease revocation limits
  explicit; historical fulfillment does not authorize dispatch by itself.
  Transient exchange additionally verifies the durable execution claim and active
  operation/task/deadline. `secret.Worker.ProvideTransient` is the internal value
  entry point; authenticated API/UI submission is not wired yet. Retain one exact
  delivery in controller memory until receipt, cancellation or expiry; changed
  submissions conflict. After a receipt, clear bytes before saving its metadata,
  retaining receipt time for uncertain-write reconciliation without resending bytes.
  Startup cancels saved pending transient requests because memory does not survive
  controller restart. Retries of terminal requests report that state without
  reopening it. Never resolve a transient name from reusable storage.
- Preserve schema validation of action input, output and proposed next actions.
  Public snapshots must remain independent.
- `action.ExecutionFromContext` exposes the current command/work identity for
  executors that need to correlate external side effects. `Agent.work` supplies
  this context and overrides caller metadata; identity is not action input or an
  authorization grant. Executors still own durable side-effect reconciliation.
- `action.Question(ctx, key, text, options...)` waits within a serving execution
  for a saved operation question answer. Stable keys bind to command/task/work;
  retries reuse the saved question and cutoff, while changed content conflicts.
  Active execution questions never hand off or resolve task continuations.
  The controller validates the durable execution claim before saving or replying.
  Operator-first timing uses the existing answer transition; the deadline alone
  supplies no answer. Caller, action and service cancellation bound the wait;
  task deadlines continue. Late replies cannot revive ended work. Unanswered
  questions remain in history after an execution ends and can require review.
  Secrets keep their separate encrypted storage, cache, versions and leases;
  never pass secret values through question text or answers.

## Validation

Run from `framework/` with the workspace disabled to check module isolation:

```sh
GOWORK=off go test -race -count=1 -timeout=3m ./...
GOWORK=off go vet ./...
```

Shared contract changes also need affected provider and app checks. Use the
[testing guide](tests/README.md) for service-backed suites and
[test-writing guidance](tests/AGENTS.md) for fixtures and failure scenarios.

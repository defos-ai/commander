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
  identify value/validity changes, not provider revisions; recreated keys get fresh IDs. Request fulfillment
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
- [`missions.RuleSet`](domain/missions/rule.go) is saved in each operation's
  mission snapshot. [`operations.RuleApplier`](domain/operations/rule_applier.go)
  evaluates it without I/O; effects commit with the triggering transition.
  Valid proposals run automatically unless a matching rule requires model review;
  failures and empty proposals also require review. Aggregation uses the current
  decision's saved child IDs. Preserve parent grouping before evaluating children,
  each child's parentage, and fresh group membership on subsequent decisions.
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
- Preserve immutable execution outcomes and reports. Save reports before
  completing operations. Agents upload bytes through `ArtifactFileStore`;
  controllers accept metadata through `ArtifactStore`. Bytes alone are not evidence.
- Keep [`AgentSelector`](controller/runner/agent_selector.go) limited to choosing
  eligible candidates. The runner owns readiness and capacity checks
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
  See the [package contract](../docs/agent-packages.md#source-template).
  Cover the shared contract in [`tests/agent/manifest`](tests/agent/manifest/).
- [`agent/packages`](agent/packages/) owns GitHub package references,
  strict discovery-catalog parsing, bounded remote catalog loading and resolution
  of `stable` to a pinned version. The official source is
  [`catalog/agents.yaml`](../catalog/agents.yaml); runtime readers fetch a configured
  remote source without requiring a checkout. Catalog entries require repository
  `visibility: public | private`; this describes access, not trust or image
  visibility. Metadata and package download clients currently remain anonymous;
  private authentication is a separate integration step. Catalog entries support discovery,
  not an enforced installation allowlist. Resolve `stable` from the repository's
  promoted root GitHub Latest release, require a stable semantic version and map
  module references to that version's module tag. Never guess or fall back to
  another release. Manifest resolution and executable downloads remain in the
  spawner; the framework must not depend on it. Cover metadata behavior and the
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

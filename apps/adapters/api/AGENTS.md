# Working on the HTTP API

## Source layout

- [api.go](api.go) assembles handlers and injected stores/commands. Host configuration,
  startup and provider construction live in [cli/](cli/); keep endpoint code free
  of concrete providers.
- Keep API types in flat `models/*_model.go` files, independent of framework models.
  Use explicit field mappings in `mappings/*_mapping.go`; do not use type aliases,
  embedding or JSON round trips to bypass the API boundary.
- Use plural collection names. Command routes and singleton views may use singular
  names. Operation routes, handlers and their OpenAPI schemas
  belong in [endpoints/operations/](endpoints/operations/). The root
  [openapi.go](openapi.go) assembles the document and shared resource schemas.
  Registered-agent handlers live in [endpoints/agents/agents.go](endpoints/agents/agents.go),
  with their schemas in [endpoints/agents/openapi.go](endpoints/agents/openapi.go). Secret
  handlers live in [endpoints/secrets/secrets.go](endpoints/secrets/secrets.go)
  and [endpoints/secrets/requests.go](endpoints/secrets/requests.go); keep their
  schemas in [endpoints/secrets/openapi.go](endpoints/secrets/openapi.go).
- Derive schemas from API models and update routes, mappings, validation, schemas
  and consumers together when API behavior changes. Keep Swagger assets local and
  documentation disabled unless the host explicitly enables it.

## Request and response rules

- Application catalog reads use the HTTP root including `/api/v1` and Basic
  credentials, configured separately from MCP. Lists return `items`; details
  return one record. Encode mission names as one path segment, including slashes
  and dot segments. Preserve typed missing-record responses; a proxy's generic
  404 is not evidence that a record is absent. Saved-state reads work without
  a live controller or MCP connection.
- Runtime-session routes use `queries.Sessions` and typed controller controls;
  see [the public contract](../../../docs/features/sessions.md#queries-and-controls).
  Keep lifecycle disposition separate from observed state and preserve retained
  history, normalized task intent and per-task placement blockers. A timed-out
  close is uncertain; a failed close can remain queued for reconciliation.
  Preserve `session_mode` across mission contracts, catalog and agent DTOs.
  Discovery accepts optional intersecting scope/owner filters and ID lookup.
  Reject unknown, repeated or empty filters. Pass criteria to the query store;
  HTTP handlers must not load scope collections to filter them themselves.
- `POST /api/v1/missions` strictly decodes a flat API request, maps it explicitly
  to a mission and uses the shared validator before `MissionStore.Create`.
  Root `objectives` is a required ordered array of nonblank strings. Preserve
  independent slices across explicit mappings.
  Optional `action_definitions` uses independent API DTOs mapped to shared action
  definitions, with cloned schema bytes and secret-name slices. Shared mission
  validation owns unique referenced names, the 100-definition limit and local
  object-schema resolution. These contracts never register actions or affect readiness.
  Existing names return `409 mission_exists`, including identical definitions;
  never emulate creation with a read followed by `Upsert`. Return the created
  mission and an escaped detail `Location`. Creation needs neither a controller
  nor action availability; it does not start work or change operation snapshots.
  Keep startup imports separate and reconcile uncertain writes by name before retrying.
- `PUT /api/v1/missions/{mission_name}` replaces an existing definition using the
  same request model and validator. Names are immutable: the body name must exactly
  match the path. Read the target before `Upsert`; absent missions return
  `404 mission_not_found`. Concurrent edits are last-write-wins. Updates affect
  future operations only, never their retained mission snapshots. Read the saved
  definition after uncertain writes; never retry an update automatically.
- Optional mission `policies.continuation` maps through independent continuation
  request/response DTOs. Clone its pointer, `aggregate_results` and `model_review`
  lists separately. Each optional list permits null or empty values, requires
  unique trimmed nonblank action names and can overlap the other list. Omitted,
  null or empty continuation declares no overrides. Keep OpenAPI nested policies
  and continuation strict; register continuation schemas before policies so the
  nested schema preserves nullable arrays and action-name constraints. Reject the
  removed `policies.placement` field, including null, through strict decoding.
- Optional mission `policies.completion` maps through independent API
  request/response DTOs to [`policies.CompletionPolicy`](../../../framework/domain/missions/policies/completion_policy.go).
  Clone its pointer at mapping boundaries; omission and explicit null stay
  omitted and default to model, while a present object requires exact
  `decision_by: model|operator` and rejects unknown fields. OpenAPI must describe
  this nested policy as optional and nullable, with no root completion property.
  Register completion schemas before policies to preserve strict nested
  constraints. Reject root `completion`, including null. Preserve the configured
  policy in catalog and operation responses. The controller enforces the saved
  operation policy: `operator` retains model output for review and permits final
  completion only with operator origin, without a timed fallback; omitted/model
  policies permit either origin to complete immediately.
- Optional mission `policies.session` uses independent session-policy DTOs with
  `create_action` and `use_actions`. Clone the group slice and each consumer list
  in both mapping directions. Register strict entry schemas before policies so
  `session` remains an optional nullable array with required nonblank creator and
  nonempty unique consumer names. Shared validation rejects overlapping groups;
  mission writes do not inspect installed agents or assign resources.
  Preserve groups through list/detail, create/update and operation snapshots.
  Controller admission injects the routing fields for mapped actions; clients
  submit business input. Keep these rules in mission OpenAPI descriptions without
  adding assignment endpoints or accepting a client-supplied authoritative binding.
- Completion requests remain output-only. Stamp `CompletedBy=CompletionOperator`
  in the trusted HTTP mapping; reject client-supplied `completed_by`. Expose the
  saved origin as optional, read-only `Operation.completed_by` and preserve it on
  queries and command responses. This records the submission channel, not human
  identity. Map `ErrCompletionOperatorRequired` to `409 completion_operator_required`
  after uncertain-write and transport errors so ambiguity cannot look like rejection.
- Operation responses expose read-only `completion_draft` as cloned raw JSON. A
  model completion request under operator policy saves it without a report; an
  authenticated HTTP completion explicitly accepts the supplied output and clears
  completion draft. Admin may review or edit the pending value before submitting
  through the same completion route. There is no separate proposal endpoint,
  identity or metadata. Preserve uncertain outcomes and require saved-state readback.
- Operation/task/report/event/log queries use the shared framework
  [`queries.Operations`](../../../framework/queries/) service; keep live requirement
  enrichment out of handlers and mappings. Commands validate API input and send
  typed controller commands. Preserve strict JSON decoding, unknown-field rejection,
  body limits, content-type checks and canonical record-ID validation.
  Question responses preserve optional `command_id` and `key` for questions
  originating from running actions. These are response provenance, not accepted
  ask-question input. Answering resumes that execution after delivery; existing
  model-decision questions retain their continuation handoff.
- The [operation log route](endpoints/operations/logs.go) requires `Config.ExecutionStore`,
  constructed from `DATABASE_URL` alongside operation storage; reuse the cached
  Mongo connection. The single `/operations/{operation_id}/logs` route accepts
  optional exact `task_id`, `agent_id`, `action_name`, plus `order`, `cursor` and
  `limit`. Validate nonempty canonical task IDs before storage and retain task
  membership checks through the shared query service. Supplied order accepts
  exactly `asc` or `desc`, defaulting to ascending durable append order. Ascending
  pages continue at positions greater than the cursor; descending pages continue
  at lower positions and remain newest first. Never sort by producer timestamps
  or reverse/truncate provider results in the handler. Keep default 100/max 1000
  bounds, typed missing-parent errors and `404 log_not_found` for unknown or
  out-of-scope cursor IDs. Return the last item ID as `next_cursor`, preserving
  the request cursor on an empty page. Live following after a descending page uses
  its first/newest ID with ascending order. Removed task-log/latest routes and
  the `latest` query parameter have no compatibility aliases. These saved queries
  do not contact the controller.
- Keep command timeouts and error mapping explicit. An uncertain response does
  not prove rejection; do not retry commands automatically or infer completion
  from client cancellation.
- Project agent/controller lease expiry when reading without rewriting saved
  records. Keep tasks/events paginated and reports immutable.
- Operation preflight preserves the controller's saved `agent_type` and
  `missing_requirements` metadata. Requirements with `kind: secret` identify
  managed declarations for assessed live candidates, not sensitive host
  configuration (`kind: configuration`, `sensitive: true`) or pending lease requests.
  Keep mappings independent; API reads never infer readiness from secret storage.
- Operation DTOs expose the shared `progress` assessment on queries and commands.
  Keep its six required fields in the OpenAPI contract and map counts explicitly;
  clients use `settled` to keep observing terminal work with unknown outcomes.
- Operation/task query DTOs add live `agent_type` and `missing_requirements` only
  for an exact current running execution. Keep this projection separate from
  persisted tasks, status and deadlines. Missing inventory omits the observation;
  cancellation still ends reads. Agent work DTOs retain their public requirements.
- Artifact downloads first establish file membership and verify saved size and
  SHA-256. Preserve sanitized attachment names, media-type fallback, empty files
  and `HEAD` behavior. Changes to buffering or range support need matching docs.

## Security boundaries

- Standalone CLI credentials come only from nonblank `API_USERNAME` and
  `API_PASSWORD`. Embedded hosts may pass in-memory credentials through
  `RunWithOptions` using `host.BasicCredentials`; reuse framework validation before
  opening listeners or provider clients.
  Keep file loading outside the standalone host. Compose's `api-credentials`
  initializer saves the password in a dedicated volume; API and Playground mount
  it read-only and export it before exec. Reuse it across restarts; explicit
  `API_PASSWORD` replaces the saved value when initialization runs again.
  Rotation requires rerunning that initializer and recreating API and Playground,
  not merely restarting their containers. Keep browser Admin login independent.
  All-in-one Playground generates its own per-session credentials.
- Only explicit local retrieval (`credentials` in the API CLI or `/credentials`
  in Playground) may display credentials; never include passwords in flags, logs,
  errors, HTTP responses or model messages. Missing standalone credentials should
  explain setup with a copyable password-generation command.
  `credentials --raw` prints only the password, preserving normal credential output.
- Secret management is separately enabled by nonempty `SECRETS_ENCRYPTION_KEY`
  (64 hex characters), parsed before opening listeners/providers. Missing means
  secret routes return authenticated `503 secrets_unavailable`; malformed means
  startup fails. Embedded callers can supply `Options.SecretCipher`, which takes
  precedence over the environment without mutating it. Reject uninitialized
  supplied ciphers before opening listeners/providers. No flag, generated
  fallback, or key/secret output in diagnostics.
  Compose's `secrets-key` initializer persists the key separately; its API wrapper
  reads the read-only mount into the environment before exec. Keep file loading
  and generation outside the standalone API host.
  `cli/secrets.go` selects FS or Mongo via `DATABASE_URL` and injects the framework
  service. `/api/v1/secrets` accepts write-only values and returns explicit metadata
  mappings. Keys contain only the shared secret name; detail routes are
  `/api/v1/secrets/{name}`. Replacements/deletions require current
  ID and version. Preserve
  `secret_write_uncertain` ahead of timeout errors; never retry automatically.
  Saving secrets makes them available to controller reconciliation; API success
  does not confirm agent installation. Keep distribution outside HTTP handlers.
- `/api/v1/secret-requests` shares the secret feature gate and database. It projects
  pending, unexpired requests into explicit metadata DTOs, sorted by request ID.
  Include effective `type` and optional execution IDs. Transient status is `pending`,
  without reading reusable secret metadata or inferring whether a value is held.
  Optional `type`, `agent_type` and `agent_id` filters intersect. Secret metadata
  has no agent type; requests retain their registered recipient type. Reject empty, repeated,
  unknown or malformed filters. Capture one time per response and never settle or
  expire requests while reading. Missing/expired values need input; available
  values await delivery unless a valid reserved delivery matches their ID/version.
  Reservation does not prove publication. This unpaginated view is eventually
  consistent across stores, not readiness or authorization evidence; refreshes may
  overlap. Never return ciphertext, installations, plaintext or key material.
  `GET /api/v1/secret-requests/{request_id}` supplies metadata-only readback of
  pending or terminal state; project overdue pending records as expired without
  changing storage. Keep terminal receipt metadata distinct from successful use.
- `POST /api/v1/secret-requests/{request_id}/values` accepts only a write-only
  UTF-8 `value`, at most 64 KiB, through the existing authenticated feature gate.
  Inject the separate `controllercommands.SecretRequests` capability; send one
  request-specific command instead of storing or delivering values in HTTP code.
  Clear owned body/value buffers. Return `202 awaiting_receipt` after publication,
  never receipt or website acceptance. Unknown IDs are 404; conflicting, closed,
  expired or reusable targets are 409. Preserve delivery/write uncertainty before
  timeout errors; replies and diagnostics must not echo values or backend errors.
  Do not retry automatically. Refresh the detail route after uncertain responses.
  Managed-secret clients request `type=reusable`; Admin's one-time form queries
  `type=transient` and submits by exact request ID, keeping codes out of the
  managed-value form. Playground support remains separate.
- `credentials` automatically reads the running container's startup environment
  only when PID 1 uses the same executable; otherwise it reads the current
  environment. Keep this lookup exclusive to explicit retrieval, never server
  configuration. Compose must exec the API as PID 1 without an init process;
  Docker exec does not inherit shell exports from another process. A matched
  process with unreadable or invalid credentials must fail without falling back.
- Preserve Basic authentication on protected routes, including unknown paths and
  enabled Swagger. Public `GET`/`HEAD` health probes, `/aboutz` and approved CORS
  preflights have deliberate exceptions; see [security.go](security.go) and [cors.go](cors.go).
- `/aboutz` exposes only `runtime` (`native`, `docker`, `docker-compose`) and
  `application` (`commander-api`, `commander`). Configure runtime explicitly with
  `API_RUNTIME`/`--runtime`; embedded Playground identifies its application through
  CLI options. Only `GET`/`HEAD` are unauthenticated and still require valid Host,
  Origin and proxy headers. Never expose secrets, paths or shell commands here.
- Origin approval must not bypass authentication, host checks or proxy trust.
  Cross-origin cookies and browser-managed credentials remain disabled.
- Trust forwarding headers only from configured peers and only in the selected
  mode. Preserve single-value validation and rejection of forwarding chains in
  [proxy.go](proxy.go).
- `/healthz` reports liveness; `/readyz` checks metadata readability. Do not imply
  the readiness probe verifies controllers, broker connectivity or artifact bytes.

## Validation

- Build/static checks run before the retained service-backed integration suites:

  ```sh
  go build ./apps/adapters/api/...
  go vet ./apps/adapters/api/...
  ```

- With the MongoDB provider test service running, verify secret persistence through
  the API host (CI runs this in the `api-full` provider phase):

  ```sh
  COMMANDER_TEST_MONGO_URL='mongodb://127.0.0.1:27017/commander_api_secret_tests?replicaSet=rs0&directConnection=true' go test -race -count=1 -timeout=3m ./apps/adapters/api/tests/host -run '^TestSecretsWithMongo$'
  ```

  This test owns only its generated secret; it does not clear shared catalogs.

- Commands run from the repository root. For public behavior, run the full API
  suite against the stack as described in [adapter guidance](../AGENTS.md#validation).
  [Integration CI](../../../.github/workflows/test-integration.yml) covers both provider stacks.
- Changes consumed by the admin UI also need its API types/calls and relevant
  checks; follow [UI guidance](../../admin/ui/AGENTS.md).

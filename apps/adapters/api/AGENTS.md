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
  saved agent sources in [endpoints/agents/sources.go](endpoints/agents/sources.go),
  and both resources' schemas in [endpoints/agents/openapi.go](endpoints/agents/openapi.go). Secret
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
- Deployment agent-source handlers and schemas live in `endpoints/agents/`.
  Require `Config.AgentSourceStore`, selected from `DATABASE_URL`. List/create use
  `/api/v1/agent-sources`; detail paths encode the full canonical
  selector as one segment. Strict creation accepts metadata only and always
  starts untrusted at version 1. Trust PATCH requires explicit `trusted` plus the
  current `version`, and performs one store CAS. Approvals require exact releases;
  `@stable` is discovery metadata. Record the authenticated actor, never a body
  claim. Reject unknown query/body fields and preserve `agent_source_write_uncertain`
  ahead of timeout/cancellation errors. No retries, seeding or controller commands.
  Trust metadata does not yet enforce installation or secret access. MCP reads
  the same store; MCP trust writes and operator confirmation are a later step.
  PATCH changes trust only; metadata remains immutable. Keep PATCH permitted by
  API CORS and the Admin proxy. Removed catalog and trust routes have no aliases.
- Runtime-session routes use `queries.Sessions` and typed controller controls;
  see [the public contract](../../../docs/runtime-sessions.md#queries-and-controls).
  Keep lifecycle disposition separate from observed state and preserve retained
  history, normalized task intent and per-task placement blockers. A timed-out
  close is uncertain; a failed close can remain queued for reconciliation.
  Preserve `session_mode` across mission contracts, catalog and agent DTOs.
  Discovery accepts optional intersecting scope/owner filters and ID lookup.
  Reject unknown, repeated or empty filters. Pass criteria to the query store;
  HTTP handlers must not load scope collections to filter them themselves.
- `POST /api/v1/missions` strictly decodes a flat API request, maps it explicitly
  to a mission and uses the shared validator before `MissionStore.Create`.
  Root `objectives` is a required ordered array of nonblank strings; steps retain
  singular `objective`. Preserve independent slices across explicit mappings.
  Optional `action_definitions` uses independent API DTOs mapped to shared action
  definitions, with cloned schema bytes and secret-name slices. Shared mission
  validation owns unique referenced names, the 100-definition limit and local
  object-schema resolution. These contracts never register actions or affect readiness.
  Existing names return `409 mission_exists`, including identical definitions;
  never emulate creation with a read followed by `Upsert`. Return the created
  mission and an escaped detail `Location`. Creation needs neither a controller
  nor action availability; it does not start work or change operation snapshots.
  Keep startup imports separate and reconcile uncertain writes by name before retrying.
- Operation/task/report/event/log queries use the shared framework
  [`queries.Operations`](../../../framework/queries/) service; keep live requirement
  enrichment out of handlers and mappings. Commands validate API input and send
  typed controller commands. Preserve strict JSON decoding, unknown-field rejection,
  body limits, content-type checks and canonical record-ID validation.
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
- Operation DTOs explicitly map the read-only `affinity` assessment from saved
  task history. Keep ownership separate from queued task assignments and live
  readiness; preserve history errors rather than presenting them as unbound.
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
  pending, unexpired requests into explicit metadata DTOs, sorted by request ID;
  optional recipient `agent_type` and `agent_id` filters intersect. Secret metadata
  has no agent type; requests retain their registered recipient type. Reject empty, repeated,
  unknown or malformed filters. Capture one time per response and never settle or
  expire requests while reading. Missing/expired values need input; available
  values await delivery unless a valid reserved delivery matches their ID/version.
  Reservation does not prove publication. This unpaginated view is eventually
  consistent across stores, not readiness or authorization evidence; refreshes may
  overlap. Never return ciphertext, installations, plaintext or key material.
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

- [tests/host/](tests/host/) covers configuration, authentication, CORS, proxies,
  health, documentation and command/file failure boundaries without a Compose stack:

  ```sh
  go test -race -count=1 -timeout=3m ./apps/adapters/api/tests/host/...
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

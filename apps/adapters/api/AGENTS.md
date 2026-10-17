# Working on the HTTP API

## Source layout

- [api.go](api.go) assembles handlers and injected stores/commands. Host configuration,
  startup and provider construction live in [cli/](cli/); keep endpoint code free
  of concrete providers.
- Keep API types in flat `models/*_model.go` files, independent of framework models.
  Use explicit field mappings in `mappings/*_mapping.go`; do not use type aliases,
  embedding or JSON round trips to bypass the API boundary.
- Use plural endpoint names. Operation routes, handlers and their OpenAPI schemas
  belong in [endpoints/operations/](endpoints/operations/). The root
  [openapi.go](openapi.go) assembles the document and catalog schemas. Secret
  handlers and schemas live together in [endpoints/secrets.go](endpoints/secrets.go)
  and [endpoints/secret_requests.go](endpoints/secret_requests.go).
- Derive schemas from API models and update routes, mappings, validation, schemas
  and consumers together when API behavior changes. Keep Swagger assets local and
  documentation disabled unless the host explicitly enables it.

## Request and response rules

- Queries use stores; commands validate API input and send typed controller
  commands. Preserve strict JSON decoding, unknown-field rejection, body limits,
  content-type checks and canonical record-ID validation.
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
- Artifact downloads first establish file membership and verify saved size and
  SHA-256. Preserve sanitized attachment names, media-type fallback, empty files
  and `HEAD` behavior. Changes to buffering or range support need matching docs.

## Security boundaries

- Standalone CLI credentials come only from nonblank `API_USERNAME` and
  `API_PASSWORD`. Embedded hosts may pass in-memory credentials through
  `RunWithOptions` using `host.BasicCredentials`; reuse framework validation before
  opening listeners or provider clients.
  Do not introduce credential files or shared passwords. Compose supplies the
  default username and generates a password at each container start unless
  explicitly supplied. Playground generates its own per-session credentials.
- Only explicit local retrieval (`credentials` in the API CLI or `/credentials`
  in Playground) may display credentials; never include passwords in flags, logs,
  errors, HTTP responses or model messages. Missing standalone credentials should
  explain setup with a copyable password-generation command.
  `credentials --raw` prints only the password, preserving normal credential output.
- Secret management is separately enabled by nonempty `SECRET_ENCRYPTION_KEY`
  (64 hex characters), parsed before opening listeners/providers. Missing means
  secret routes return authenticated `503 secrets_unavailable`; malformed means
  startup fails. No flag, generated fallback, or key/secret output in diagnostics.
  `cli/secrets.go` selects FS or Mongo via `DATABASE_URL` and injects the framework
  service. `/api/v1/secrets` accepts write-only values and returns explicit metadata
  mappings. Keys contain the agent type and secret name; detail routes are
  `/api/v1/secrets/{agent_type}/{name}`. Replacements/deletions require current
  ID and version. Preserve
  `secret_write_uncertain` ahead of timeout errors; never retry automatically.
  Saving secrets makes them available to controller reconciliation; API success
  does not confirm agent installation. Keep distribution outside HTTP handlers.
- `/api/v1/secrets/requests` shares the secret feature gate and database. It projects
  pending, unexpired requests into explicit metadata DTOs, sorted by request ID;
  optional `agent_type` and `agent_id` filters intersect. Reject empty, repeated,
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

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
  [openapi.go](openapi.go) assembles the document and catalog schemas.
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
- Artifact downloads first establish file membership and verify saved size and
  SHA-256. Preserve sanitized attachment names, media-type fallback, empty files
  and `HEAD` behavior. Changes to buffering or range support need matching docs.

## Security boundaries

- Standalone CLI credentials come only from nonblank `API_USERNAME` and
  `API_PASSWORD`. Embedded hosts may pass in-memory credentials through
  `RunWithOptions` using `host.Credentials`; reuse framework validation before
  opening listeners or provider clients.
  Do not introduce credential files or shared passwords. Compose supplies the
  default username and generates a password at each container start unless
  explicitly supplied. Playground generates its own per-session credentials.
- Only explicit local retrieval (`credentials` in the API CLI or `/credentials`
  in Playground) may display credentials; never include passwords in flags, logs,
  errors, HTTP responses or model messages. Missing standalone credentials should
  explain setup with a copyable password-generation command.
- `credentials --pid` reads process environment only for explicit retrieval, never
  for server configuration. Compose must exec the API as PID 1 without an init
  process so `credentials --pid 1` finds the shell-generated environment; Docker
  exec does not inherit shell exports from another process.
- Preserve Basic authentication on protected routes, including unknown paths and
  enabled Swagger. Public `GET`/`HEAD` health probes and approved CORS preflights
  have deliberate exceptions; see [security.go](security.go) and [cors.go](cors.go).
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

- Commands run from the repository root. For public behavior, run the full API
  suite against the stack as described in [adapter guidance](../AGENTS.md#validation).
  [Integration CI](../../../.github/workflows/test-integration.yml) covers both provider stacks.
- Changes consumed by the admin UI also need its API types/calls and relevant
  checks; follow [UI guidance](../../admin/ui/AGENTS.md).

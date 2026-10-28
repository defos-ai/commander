# Working on Admin

Admin is one Go module rooted here. Read the [root guide](../../AGENTS.md) and
the [UI guide](ui/AGENTS.md) for UI changes.

- Keep UI backend packages under `ui/backend/` and frontend sources under
  `ui/frontend/`. Do not add nested Go modules for individual interfaces.
- `cmd/commander-admin` is the standalone command entry point. `cli.RunWithOptions`
  accepts a caller-owned connection for embedding; only `Main`
  owns process signals and exit codes. Help must work without API/model setup.
  Keep argument errors distinct from runtime failures. JSON output describes
  command data; help remains text. Mission, secret and session options work around
  subcommands and operands; `--` makes later values literal, including
  dash-prefixed names. UI flags follow `serve`.
- `serve` delegates to `ui/backend/cli`; it uses `UI_*` settings and must not
  load or validate `COMMANDER_API_*` credentials. Browser connections remain
  independent of the CLI API connection. Preserve the host's ready callback,
  caller-owned context and graceful shutdown for Playground embedding.
- `client/` owns the shared authenticated API transport. Connections come from
  explicit options or `COMMANDER_API_*`, never MCP inference. Do not accept a
  password flag, print credentials or expose raw network/server errors. Keep
  redirects/proxies disabled, responses bounded and writes free of automatic retries.
  `client.ConnectionError` identifies local connection setup; the CLI's
  `SecretInputRequired` lets an embedded caller open a masked form after metadata
  is read. Keep these distinguishable from failed or uncertain API writes.
- `missions/` validates mission files with the shared framework loader and sends
  create-only writes through the API. Retain raw definitions when importing or
  exporting so schema numbers, escapes and extension keywords survive. Validate
  the complete batch and reject duplicate names before API access. Files are
  bounded to 4 MiB and each imported definition to the API's 1 MiB request limit;
  response validation uses the client's 4 MiB bound because API-added fields can
  enlarge a saved definition.
- Import checks names individually: a confirmed existing definition is skipped;
  only `404 mission_not_found` permits a create. A racing `409 mission_exists`
  also preserves the winner. Dry runs send no writes. Stop on the first failure,
  preserve completed results and report remaining items as not attempted. An
  uncertain acknowledgement requires inspection, never an automatic write retry.
  Import does not start operations, require installed actions or replace missions.
- `missions.Seed` embeds [bundled missions](missions/default-missions.json), validates
  the full batch and delegates to the same create-only API import. Keep `missions
  seed [--dry-run]` and launcher calls on this path; the controller does not own
  catalog initialization. Existing definitions always win, including operator edits.
- Keep bundled mission policy in that JSON. `review-website` starts HTTP discovery
  and aggregates its checks before model review. Browser accessibility and screenshots
  are optional; add ready checks through the discovery continuation and report skipped
  coverage. `install-agent` requires only `spawn-agent`
  and reviews the launch result and readiness; GitHub stable resolution belongs to
  the spawner, and visibility travels unchanged without tokens in mission input.
- `build-agent` requires operator action contracts alongside instructions, checkout
  before build, workspace affinity on those two actions and model review on their
  results. Publication/release stay optional, with no workspace affinity, so their
  secrets cannot block a build. Publication needs consent; release needs its own
  consent/version. Release polls continue automatically until terminal model review.
  Installation is offered only after verified release, with explicit operator consent,
  then runs in a separate `install-agent` operation. Preserve successful build evidence
  if publication, release or installation is declined or fails.
- `secrets/` owns the shared secret API operations used by CLI and Playground.
  Project validated metadata instead of returning raw responses. Only a typed
  `404 secret_not_found` permits creation; updates/deletes carry the observed ID
  and version. Preserve future expiry and renew expired values without expiry.
  Require exact success statuses and bound individual metadata responses to
  64 KiB. Conflicts require fresh metadata; unconfirmed writes are never retried.
- `sessions/` owns validated session views and API controls shared by CLI and
  Playground. Inspect uses ID lookup; controls resolve its exact saved scope and
  process owner before sending one POST. Preserve the submitted request ID in
  results/errors and uncertainty through `errors.Is`; an existing pending close
  can have its own controller-owned identity. Never retry a control automatically
  or interpret a successful request as confirmed closure. Display observed state
  and retain abandonment's unconfirmed-closure warning. Session filters and
  request-ID flags apply only to their corresponding session commands.
- Secret commands detect terminal input: use a hidden prompt for terminals and
  read piped/redirected input directly, never arguments or model input. Do not
  probe for available bytes; pipes may deliver later. Read metadata before
  requesting a value. Keep stdin bytes unchanged, clear transient buffers, and
  return only metadata. Playground uses the same connection and services through
  `/admin`; its masked form owns login/value entry rather than terminal stdin.
  Keep automatic requirement triggering, dismissal and draft isolation intact.
- [docker/Dockerfile](docker/Dockerfile) builds `cmd/commander-admin` with embedded
  frontend assets. The image entry point is `commander-admin`, with `serve` as
  its default command; replacing the command must expose mission/secret/session commands.
  Do not restore a separate `commander-ui` binary or image.
- Playground imports the UI host from this module. Update its module requirement,
  workspace mapping and CI selection when changing Admin dependencies.
- Keep module-wide Go checks rooted at `apps/admin`; frontend commands still run
  from `ui/frontend`. Use the UI guide for packaging and integration checks.
- Keep CLI/client tests in `tests/`, using public packages and bounded local HTTP
  fixtures. Keep setup/data in `tests/fixtures` and fake implementations in
  `tests/fakes`; follow the shared [test rules](../../framework/tests/AGENTS.md).
  Do not add paid-model or Docker dependencies to command tests.

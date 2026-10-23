# Working on Admin

Admin is one Go module rooted here. Read the [root guide](../../AGENTS.md) and
the [UI guide](ui/AGENTS.md) for UI changes.

- Keep UI backend packages under `ui/backend/` and frontend sources under
  `ui/frontend/`. Do not add nested Go modules for individual interfaces.
- `cmd/commander-admin` is the standalone command entry point. `cli.RunWithOptions`
  accepts a caller-owned connection for embedding; only `Main`
  owns process signals and exit codes. Help must work without API/model setup.
  Keep argument errors distinct from runtime failures. Add JSON data output with
  resource commands, not a second machine-readable help format.
- `client/` owns the shared authenticated API transport. Connections come from
  explicit options or `COMMANDER_API_*`, never MCP inference. Do not accept a
  password flag, print credentials or expose raw network/server errors. Keep
  redirects/proxies disabled, responses bounded and writes free of automatic retries.
- [docker/Dockerfile](docker/Dockerfile) builds the existing UI host with embedded
  frontend assets. The published image remains `commander-admin`.
- Playground imports the UI host from this module. Update its module requirement,
  workspace mapping and CI selection when changing Admin dependencies.
- Keep module-wide Go checks rooted at `apps/admin`; frontend commands still run
  from `ui/frontend`. Use the UI guide for packaging and integration checks.
- Keep CLI/client tests in `tests/`, using public packages and bounded local HTTP
  fixtures. Follow [test guidance](../../framework/tests/AGENTS.md); do not add
  paid-model or Docker dependencies to command tests.

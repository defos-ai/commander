# Testing

Two suites run in GitHub Actions:

- [Unit tests](../../.github/workflows/test-unit.yml): framework, providers,
  controller, agent and Playground tests, including its `--all-in-one` behavior.
- [MCP API tests](../../.github/workflows/test-mcp.yml): real Compose services,
  queried only through MCP. Uses the same start, test and cleanup commands below,
  with service logs saved on failure.

No model API key or public website is needed.

## Unit tests

From the repository root:

```sh
go test -race -count=1 ./framework/... ./providers/... \
  ./apps/controllers/default/... ./apps/agents/web-quality/... ./apps/playground/...
```

Framework tests focus on recovery, automation and concurrent decisions. Provider
tests cover critical storage and delivery contracts. App tests cover mission
loading, website checks, rendering and Playground's local hosts. These tests
need no Docker or browser; browser execution is covered by the MCP suite.

## MCP API tests

From the repository root, run these three commands:

```sh
docker compose -f compose.test.yaml up --build --wait
go test -count=1 ./apps/adapters/mcp/tests
docker compose -f compose.test.yaml down --volumes
```

`compose.test.yaml` is standalone. It uses the fixed project `commander-test`,
its own volumes and images, and MCP at `http://127.0.0.1:14273/mcp`.
Tests use that endpoint automatically; no environment setup is needed.
`--wait` starts services in the background and waits for readiness.
Run the cleanup command even if tests fail.

For service logs, use `docker compose -f compose.test.yaml logs`.
CI saves logs on failure and always removes the test containers and volumes.

### Test groups

Tests live under [`apps/adapters/mcp/tests`](../../apps/adapters/mcp/tests):

- `missions_test.go`: catalog, input validation and caller-provided IDs.
- `agents_test.go`: agent/action catalogs, discovery, errors and execution.
- `operations_test.go`: automatic website review, grouped decisions, artifacts,
  event cursors, reports, cancellation and failure.
- `questions_test.go`: latest question, operator answers and expiration.

Before each test, setup calls the MCP `clean_up_state` tool. It clears database
and storage test data, restores configured missions and waits for agent heartbeats
to repopulate inventory. Bus messages, subscriptions and host memory are preserved.
The tool is registered only with `ENV=tests` in the test Compose stack and uses
`framework/testing.TestCleaner`, without changing production store interfaces.

Tests run sequentially and must finish their work before the next cleanup; this
is persisted-data cleanup, not a full runtime reset. The Go fixture only uses MCP
and never invokes Docker or reads shared volumes. Compose lifecycle belongs to the
three commands above and the CI workflow; volume removal is final housekeeping.

The test stack includes two agents with Chromium and the default mission;
website fixtures are served by the tests and reached through `host.docker.internal`.

## Writing tests

- Use **Arrange, Act, Assert** and public interfaces.
- Add an MCP API scenario for behavior spanning apps; keep shared setup in
  `fixture_test.go` and one file per feature group.
- Add unit tests for complex invariants and failure paths, rather than mirroring
  implementation details or retesting every constructor.
- Keep tests in each module's `tests/` folder where possible.
- Use deterministic fixtures, bounded waits and disposable state.

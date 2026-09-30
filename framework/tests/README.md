# Testing

Three suites run in GitHub Actions:

- [Unit tests](../../.github/workflows/test-unit.yml): framework, controller,
  agent and Playground tests, including its `--all-in-one` behavior.
- [Provider tests](../../.github/workflows/test-provider.yml): separate FS and
  Mongo jobs covering storage and delivery contracts.
- [MCP API tests](../../.github/workflows/test-mcp.yml): real Compose services,
  queried only through MCP. Uses the same start, test and cleanup commands below,
  with service logs saved on failure.

No model API key or public website is needed.

## Unit tests

From the repository root:

```sh
go test -race -count=1 ./framework/... \
  ./apps/controllers/default/... ./apps/agents/web-quality/... ./apps/playground/...
```

Framework tests focus on recovery, automation, concurrent decisions and retrying
unconfirmed results without executing commands twice. App tests cover mission
loading, website checks, rendering and Playground's local hosts. Playground also
completes an SEO review through a scripted model, keyboard-selected operator
answer and saved report, and checks recovery after model errors or Ctrl+C. These tests
need no Docker or browser; browser execution is covered by the MCP suite.

## Provider tests

Run the FS suite from the repository root:

```sh
go test -race -count=1 ./providers/fs/...
```

The separate Mongo module uses a real database. See the
[Mongo testing guide](../../providers/mongo/README.md#development-and-testing)
for its Compose and test commands. CI starts and cleans up Mongo automatically.

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
- `agents_test.go`: catalogs, execution, and queued work resuming after a missing
  dependency becomes available through rediscovery.
- `operations_test.go`: automatic website review, grouped decisions, artifacts,
  event cursors, reports, concurrent operation isolation, capacity limits,
  cancellation during execution and failure.
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

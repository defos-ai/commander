# Testing

Use MCP API tests for behavior across hosts and unit tests for complex invariants.
Tests use local fixtures and scripted models; no model API key or public website
is required. Run commands below from the repository root.

## Unit tests

Choose the affected module:

```sh
go test -race -count=1 -timeout=3m ./framework/...
go test -race -count=1 -timeout=3m ./apps/controllers/default/...
go test -race -count=1 -timeout=3m ./apps/agents/web-quality/...
go test -race -count=1 -timeout=3m ./apps/playground/...
```

Framework tests cover recovery, atomic decisions, automation and result delivery.
App tests cover mission loading, actions and client behavior. Playground includes
an all-in-one workflow using a scripted model, operator answers and a saved report.
These tests need no real model or browser; MCP tests cover browser execution.

## Provider tests

```sh
go test -race -count=1 -timeout=3m ./providers/fs/...
```

Mongo and S3 suites use real local services. Follow their setup/test commands:
[MongoDB](../../providers/mongo/README.md#development-and-testing) ·
[S3/MinIO](../../providers/s3/README.md#development-and-testing).

## MCP API tests

```sh
docker compose -f compose.test.yaml up --build --wait
go test -race -count=1 -failfast -timeout=5m -v ./apps/adapters/mcp/tests
docker compose -f compose.test.yaml down --volumes
```

Run the cleanup command even if tests fail. The standalone stack uses project
`commander-test`, separate volumes, two agents with Chromium, and MCP at
`http://127.0.0.1:14273/mcp`. Tests use that endpoint automatically. For logs:
`docker compose -f compose.test.yaml logs`.

| Test group | Covers |
| --- | --- |
| `missions_test.go` | Catalog, validation and caller-supplied operation IDs. |
| `agents_test.go` | Agent/action visibility and dependency rediscovery. |
| `operations_test.go` | Website workflow, continuations, artifacts, reports, isolation, capacity and cancellation. |
| `questions_test.go` | Latest question, answering and expiration. |

Before each test, `clean_up_state` clears saved test data, restores configured
missions and waits for inventory. This tool exists only with `ENV=tests`.
**It preserves bus messages, subscriptions and host memory.** Tests run sequentially
and finish their work before the next cleanup. The Go fixture uses MCP; it never
controls Docker or reads shared volumes.

The additional Mongo startup check runs when `COMMANDER_TEST_MONGO_URL` is set;
CI supplies a replica-set connection. The main API scenarios use the FS test stack.

## CI

Each suite has a separate workflow and a manual **Run workflow** entry:

| Code changed | Test suites triggered |
| --- | --- |
| Framework | All |
| FS / S3 provider | That provider, controller, agent, MCP, Playground |
| Mongo provider | Mongo, controller, MCP, Playground |
| Controller | Controller, MCP, Playground |
| Agent | Agent, MCP, Playground |
| MCP | MCP, Playground |
| Playground | Playground |

Workspace/module dependency changes run all suites. Markdown-only changes do not.
Compose and Dockerfile changes trigger the relevant integration suites. See
[workflow definitions](../../.github/workflows) for the exact filters.
CI bounds test runs, collects failed-service logs and removes test containers.

## Writing tests

- Follow **Arrange, Act, Assert** through public behavior.
- Add MCP scenarios for cross-host behavior, one file per feature group.
- Unit-test complex invariants and failure paths; avoid mirroring implementation.
- Keep tests under each module's `tests/` directory where possible.
- Use deterministic fixtures, bounded waits and disposable state.

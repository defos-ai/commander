# Testing

Use HTTP and MCP tests for behavior across hosts and unit tests for complex invariants.
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
These tests need no real model or browser; adapter tests cover browser execution.

## Provider tests

```sh
go test -race -count=1 -timeout=3m ./providers/fs/...
```

Mongo, S3 and RabbitMQ suites use real local services. Follow their setup/test commands:
[MongoDB](../../providers/mongo/README.md#development-and-testing) ·
[S3/MinIO](../../providers/s3/README.md#development-and-testing) ·
[RabbitMQ](../../providers/rabbitmq/README.md#development-and-testing).

## MCP API tests

```sh
export COMPOSE_FILE=compose.test.yaml # or compose.full.test.yaml
docker compose up --build --wait
go test -race -count=1 -failfast -p 1 -timeout=5m -v ./apps/adapters/api/... ./apps/adapters/mcp/...
docker compose down --volumes
```

Both suites use external clients against the same services. Choose the FS stack
(`commander-test`) or MongoDB/RabbitMQ/MinIO stack (`commander-full-test`); each
has isolated volumes and two browser-enabled agents. Run only one stack locally
at a time: both expose MCP on `14273` and HTTP API on `14274`.

Tests use these endpoints and test credentials automatically. Override with
`COMMANDER_TEST_MCP_URL`, `COMMANDER_TEST_API_URL`,
`COMMANDER_TEST_API_USERNAME` and `COMMANDER_TEST_API_PASSWORD` when needed.
Use `docker compose logs` to diagnose failures, and always run `down --volumes`.

| Test group | Covers |
| --- | --- |
| `missions_test.go` | Catalog, validation and caller-supplied operation IDs. |
| `agents_test.go` | Agent/action visibility and dependency rediscovery. |
| `controllers_test.go` (HTTP API) | Controller registration, readiness and list/detail queries. |
| `operations_test.go` | Website workflow, continuations, artifacts, reports, isolation, capacity and cancellation. |
| `questions_test.go` | Latest question, answering and expiration. |

Before each test, fixtures settle previous work and call MCP `clean_up_state` or
HTTP `POST /api/v1/testing/cleanup`. Both exist only with `ENV=tests` and reject
cleanup while work is active. Cleanup clears operations, reports and artifacts;
it preserves missions, controller registrations, agent/action catalogs, delivery records, bus and host memory.
Retired artifact IDs prevent late result replays from restoring deleted metadata.

Tests run sequentially; `-p 1` also keeps the two adapter suites from sharing the
stack concurrently. Fixtures use only public endpoints, never Docker or shared
volumes. Small `tests/host` suites cover local CLI, HTTP security and configuration.

## CI

Each suite has a separate workflow and a manual **Run workflow** entry:

| Code changed | Test suites triggered |
| --- | --- |
| Framework | All |
| FS / S3 provider | That provider, controller, agent, API, MCP, Playground |
| Mongo provider | Mongo, controller, API, MCP, Playground |
| RabbitMQ provider | RabbitMQ, controller, agent, API, MCP, Playground |
| Controller | Controller, API, MCP, Playground |
| Agent | Agent, API, MCP, Playground |
| API | API, MCP |
| MCP | API, MCP, Playground |
| Playground | Playground |

Workspace/module dependency changes run all suites. Markdown-only changes do not.
Compose and Dockerfile changes trigger the relevant integration suites. See
[workflow definitions](../../.github/workflows) for the exact filters.
CI bounds test runs, collects failed-service logs and removes test containers.
The API and MCP workflows each run the same suite against FS and full providers.

## Writing tests

- Follow **Arrange, Act, Assert** through public behavior.
- Add HTTP or MCP scenarios for cross-host behavior, one file per feature group.
- Unit-test complex invariants and failure paths; avoid mirroring implementation.
- Keep tests under each module's `tests/` directory where possible.
- Use deterministic fixtures, bounded waits and disposable state.

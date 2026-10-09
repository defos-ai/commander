# Testing

Tests use local fixtures and scripted models; no model API key or public website
is required. Run commands below from the repository root. Coding and test-writing
guidance lives in [AGENTS.md](AGENTS.md).

## Unit tests

Choose the affected module:

```sh
go test -race -count=1 -timeout=3m ./framework/...
go test -race -count=1 -timeout=3m ./apps/controllers/default/...
go test -race -count=1 -timeout=3m ./apps/agents/browser/...
go test -race -count=1 -timeout=3m ./apps/agents/web/...
go test -race -count=1 -timeout=3m ./apps/playground/...
go vet ./framework/...
```

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
export COMPOSE_FILE=.github/composes/compose.test.yaml # or .github/composes/compose.full.test.yaml
docker compose up --build --wait
go test -race -count=1 -failfast -p 1 -timeout=5m -v ./apps/adapters/api/... ./apps/adapters/mcp/...
docker compose down --volumes
```

Choose the FS stack (`commander-test`) or MongoDB/RabbitMQ/MinIO stack
(`commander-full-test`). Each has isolated volumes, two web agents and one
browser agent with Chromium.
Run only one stack locally at a time: both expose MCP on `14273` and HTTP API on
`14274`. Keep `-p 1` so the two adapter suites use the stack sequentially.

Tests use these endpoints and test credentials automatically. Override with
`COMMANDER_TEST_MCP_URL`, `COMMANDER_TEST_MCP_TOKEN`, `COMMANDER_TEST_API_URL`,
`COMMANDER_TEST_API_USERNAME` and `COMMANDER_TEST_API_PASSWORD` when needed.
Use `docker compose logs` to diagnose failures, and always run `down --volumes`.

## CI

[test.yml](../../.github/workflows/test.yml) selects checks using
[path filters](../../.github/test-paths.yml): framework and shared CI changes run
everything; provider and application changes run their checks and affected consumers.
Markdown-only changes skip component tests. Manual **Run workflow** and releases run every suite.

A lightweight configuration check runs first, catching new components missing
workspace registration, a path filter or a selected test command. Run it locally:

```sh
(cd .github/tests && GOWORK=off go test -count=1 -timeout=1m -run '^TestCI' .)
```

Selected [unit checks](../../.github/workflows/test-unit.yml) must pass before
[integration checks](../../.github/workflows/test-integration.yml) start.
One Docker runner reuses images across provider, FS and full-stack tests. Each
phase has separate state; API, MCP and browser suites run sequentially.
Failures retain logs and browser traces. Use **Tests passed** as the required
check so intentionally skipped suites do not block documentation-only changes.

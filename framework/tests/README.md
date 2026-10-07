# Testing

Tests use local fixtures and scripted models; no model API key or public website
is required. Run commands below from the repository root. Coding and test-writing
guidance lives in [AGENTS.md](AGENTS.md).

## Unit tests

Choose the affected module:

```sh
go test -race -count=1 -timeout=3m ./framework/...
go test -race -count=1 -timeout=3m ./apps/controllers/default/...
go test -race -count=1 -timeout=3m ./apps/agents/web-quality/...
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
export COMPOSE_FILE=compose.test.yaml # or compose.full.test.yaml
docker compose up --build --wait
go test -race -count=1 -failfast -p 1 -timeout=5m -v ./apps/adapters/api/... ./apps/adapters/mcp/...
docker compose down --volumes
```

Choose the FS stack (`commander-test`) or MongoDB/RabbitMQ/MinIO stack
(`commander-full-test`). Each has isolated volumes and browser-enabled agents.
Run only one stack locally at a time: both expose MCP on `14273` and HTTP API on
`14274`. Keep `-p 1` so the two adapter suites use the stack sequentially.

Tests use these endpoints and test credentials automatically. Override with
`COMMANDER_TEST_MCP_URL`, `COMMANDER_TEST_API_URL`,
`COMMANDER_TEST_API_USERNAME` and `COMMANDER_TEST_API_PASSWORD` when needed.
Use `docker compose logs` to diagnose failures, and always run `down --volumes`.

## CI

Each [workflow](../../.github/workflows/) supports manual **Run workflow**.
Change filters select affected suites; Markdown-only changes do not run tests.
API and MCP workflows test both FS and full-provider stacks and collect service
logs on failure.

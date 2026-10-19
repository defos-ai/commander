# Testing

Tests use local fixtures and scripted models; no model API key or public website
is required. Run commands below from the repository root. Coding and test-writing
guidance lives in [AGENTS.md](AGENTS.md).

## Unit tests

Choose the affected module:

```sh
go test -race -count=1 -timeout=3m ./framework/...
go test -race -count=1 -timeout=3m ./apps/controllers/default/...
go test -race -count=1 -timeout=3m ./apps/agents/builder/...
go test -race -count=1 -timeout=3m ./apps/agents/spawner/...
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
export COMPOSE_FILE=.github/composes/compose.test.yaml
docker compose up --build --wait
go test -race -count=1 -failfast -p 1 -timeout=5m -v ./apps/adapters/api/... ./apps/adapters/mcp/...
docker compose down --volumes
```

The FS stack (`commander-test`) has isolated volumes, two web agents, one
browser agent with Chromium from pinned standalone release images, and a native
spawner. Startup provisions one fixture
through `--spawn-agents`; API/MCP scenarios provision another through an operation,
run an action and retrieve its artifact. A local HTTPS release server supplies the
fixture manifest and binary. Web/browser images are pulled from GHCR during setup.

For MongoDB/RabbitMQ/MinIO and Docker spawning (`commander-full-test`):

```sh
export COMPOSE_FILE=.github/composes/compose.full.test.yaml
docker compose build
docker compose build fixture-agent
docker compose up --wait registry
docker tag commander-fixture-agent:test localhost:15000/fixture-agent:fixture
docker push localhost:15000/fixture-agent:fixture
docker compose up --no-build --wait
COMMANDER_TEST_SPAWNER_RUNTIME=docker go test -race -count=1 -failfast -p 1 -timeout=5m -v ./apps/adapters/api/... ./apps/adapters/mcp/...
docker compose stop spawner-agent
docker ps -aq --filter network=commander-full-test_default --filter label=ai.defos.commander.spawn-owner | xargs -r docker rm -f
docker compose down --volumes
```

The registry is bound to loopback. Setup publishes only the fixture image there;
the spawner pulls its digest and launches a sibling container. Docker spawning
requires the host Docker socket. Its containers outlive the spawner, so remove
those on the test network before taking the stack down, including after failures.

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
phase has separate state; API and MCP suites run sequentially.
Web/browser unit and browser end-to-end suites run in their own repositories.
Failures retain stack logs. Use **Tests passed** as the required
check so intentionally skipped suites do not block documentation-only changes.

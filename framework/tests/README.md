# Testing

Run commands from the repository root. Tests use local fixtures and scripted
models; no paid model calls or public website is needed.

## Unit tests

Choose the affected module, for example:

```sh
go test -race -count=1 -timeout=3m ./framework/...
go test -race -count=1 -timeout=3m ./apps/agents/builder/...
go vet ./framework/...
```

Use the same commands with another module path from [go.work](../../go.work).

## Provider tests

```sh
go test -race -count=1 -timeout=3m ./providers/fs/...
```

For service-backed tests, follow the setup commands in
[MongoDB](../../providers/mongo/README.md#development-and-testing),
[S3/MinIO](../../providers/s3/README.md#development-and-testing) or
[RabbitMQ](../../providers/rabbitmq/README.md#development-and-testing).

## MCP API tests

The stacks use the lightweight example agent for routing, proposed actions and
artifact checks. Web/browser-agent tests run in their own repositories.

Start the FS stack, run suites sequentially, then remove test state:

```sh
export COMPOSE_FILE=.github/composes/compose.test.yaml
docker compose up --build --wait
go test -race -count=1 -failfast -p 1 -timeout=5m -v ./apps/adapters/api/... ./apps/adapters/mcp/...
docker compose down --volumes
```

For MongoDB, RabbitMQ, MinIO and Docker spawning:

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

Run one stack at a time; their ports overlap. The full stack needs the host Docker
socket. Remove spawned test containers and volumes even after failures. For
failure details, run `docker compose logs` before teardown.

Test endpoints and credentials are preconfigured. Override them with
`COMMANDER_TEST_MCP_URL`, `COMMANDER_TEST_MCP_TOKEN`, `COMMANDER_TEST_API_URL`,
`COMMANDER_TEST_API_USERNAME` and `COMMANDER_TEST_API_PASSWORD` when needed.

## CI

[CI](../../.github/workflows/test.yml) selects affected suites using
[path filters](../../.github/test-paths.yml). Manual and release runs select all
suites. Use **Tests passed** as the required branch check.

After changing workflow wiring, validate it locally:

```sh
(cd .github/tests && GOWORK=off go test -count=1 -timeout=1m -run '^TestCI' .)
```

See [AGENTS.md](AGENTS.md) before adding fixtures or changing shared-stack tests.

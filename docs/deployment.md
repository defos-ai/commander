# Local setup and configuration

Start with [the root quick start](../README.md#quick-start). Compose is a local
development setup; production deployment is not yet documented.

## Local Compose

From the repository root, start the stack and launch Playground:

```sh
docker compose up --build --wait
docker compose run --build --rm --no-deps playground
```

Compose runs MCP, the API and admin UI, one controller and two web-quality agents.
It uses RabbitMQ for messaging, MongoDB for the database, and MinIO for file
storage. See [RabbitMQ](../providers/rabbitmq/README.md#quick-start),
[MongoDB](../providers/mongo/README.md), and [S3](../providers/s3/README.md) for
connection details. Bundled infrastructure credentials are for local development.

Connect another AI client to `http://localhost:4273/mcp` with the same bearer token using the
[adapter guide](../apps/adapters/README.md#quick-start). For a temporary session
without Compose, use [Playground's all-in-one mode](../apps/playground/README.md#native-one-executable),
which generates and shares its MCP token automatically.

The API listens on `4274` and admin UI on `4275`. Compose generates an API password
and MCP token on each container start; set `API_PASSWORD` and `MCP_TOKEN` to
preserve your own credentials. The
[UI quick start](../apps/admin/ui/README.md#quick-start) shows retrieval and port overrides.
Use `docker compose exec mcp commander-mcp credentials` to look up the MCP token.
Paste the token when Playground prompts; after MCP restarts, retrieve its new token.
For an environment one-liner, see [Playground setup](../apps/playground/README.md#docker-recommended).
Credentials are kept in process environments; no credential files are created.

## Configuration

| Environment variable | CLI flag | Purpose |
| --- | --- | --- |
| `BUS_URL` | `--bus-url` | Messaging |
| `DATABASE_URL` | `--database-url` | Controller/MCP database |
| `STORAGE_URL` | `--storage-url` | Artifact files and reports |
| `LOG_LEVEL` | `--log-level` | Log verbosity |
| `LOG_FORMAT` | `--log-format` | `text` or `json` logs |
| `MCP_TOKEN` | — | Required standalone MCP bearer token |
| `COMMANDER_MCP_TOKEN` | — | Playground token for an existing MCP server |

Flags override environment values. Native hosts default to `./state/bus`,
`./state/database`, and `./state/storage`. Agents need bus and file storage only.
Compose supplies backend settings; application images do not choose providers.
`COMMANDER_MCP_PORT` changes the published Compose port (default `4273`).
Run a host with `--help` for its flags. Action-specific dependencies belong in the
[agent guide](../apps/agents/web-quality/README.md); model settings belong in
[Playground](../apps/playground/README.md).

## State

Compose creates named Docker volumes; no host `state/` directory is needed.
It persists RabbitMQ, MongoDB, and MinIO data in their own volumes;
application hosts need no shared filesystem volumes. Host inboxes remain
transient: broker persistence does not preserve a replaced host's inbox.

Compose passes `--with-missions` to load the example mission. This upserts supplied
missions without removing others; without the flag the controller loads none.
See [mission configuration](../apps/controllers/README.md#quick-start).

Use `docker compose logs -f` for logs and `docker compose down` to stop.
**`docker compose down --volumes` deletes saved state.**
For restart behavior and scaling limits, see [architecture](overview.md#state-and-restart-recovery).

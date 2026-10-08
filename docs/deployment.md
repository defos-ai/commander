# Local setup and configuration

Start with [the root quick start](../README.md#quick-start). These Compose stacks
are local development setups; production deployment is not yet documented.

## Local Compose

Choose one stack from the repository root. Each runs MCP, one controller and two
web-quality agents; Playground connects as a separate interactive client.

**Filesystem:**

```sh
docker compose up --build -d
docker compose run --build --rm playground
```

**RabbitMQ + MongoDB + MinIO:**

```sh
docker compose -f compose.full.yaml up --build -d
docker compose -f compose.full.yaml run --build --rm playground
```

Full Compose uses RabbitMQ for messaging, MongoDB for the database, and MinIO
for file storage. The stacks use separate projects (`commander` and
`commander-full`), but share the default MCP port. See [RabbitMQ](../providers/rabbitmq/README.md#quick-start),
[MongoDB](../providers/mongo/README.md), and [S3](../providers/s3/README.md) for
connection details. Bundled infrastructure credentials are for local development.

Connect another AI client to `http://localhost:4273/mcp` using the
[adapter guide](../apps/adapters/README.md#quick-start). For a temporary session
without Compose, use [Playground's all-in-one mode](../apps/playground/README.md#native-one-executable).

Add `--profile admin` to either stack's `up` command for the API (`4274`) and
admin UI (`4275`). Compose generates an API password on each container start;
set `API_PASSWORD` for a stable password. The
[UI quick start](../apps/admin/ui/README.md#quick-start) shows retrieval and port overrides.

## Configuration

| Environment variable | CLI flag | Purpose |
| --- | --- | --- |
| `BUS_URL` | `--bus-url` | Messaging |
| `DATABASE_URL` | `--database-url` | Controller/MCP database |
| `STORAGE_URL` | `--storage-url` | Artifact files and reports |
| `LOG_LEVEL` | `--log-level` | Log verbosity |
| `LOG_FORMAT` | `--log-format` | `text` or `json` logs |

Flags override environment values. Native hosts default to `./state/bus`,
`./state/database`, and `./state/storage`. Agents need bus and file storage only.
Compose supplies backend settings; application images do not choose providers.
`COMMANDER_MCP_PORT` changes the published Compose port (default `4273`).
Run a host with `--help` for its flags. Action-specific dependencies belong in the
[agent guide](../apps/agents/web-quality/README.md); model settings belong in
[Playground](../apps/playground/README.md).

## State

Compose creates named Docker volumes; no host `state/` directory is needed.
The FS stack shares `bus` and `storage` across hosts, and `database` between the
controller, MCP and optional API. A startup helper sets volume ownership for non-root hosts.
Full Compose persists RabbitMQ, MongoDB, and MinIO data in their own volumes;
application hosts need no shared filesystem volumes. Host inboxes remain
transient: broker persistence does not preserve a replaced host's inbox.

Compose passes `--with-missions` to load the example mission. This upserts supplied
missions without removing others; without the flag the controller loads none.
See [mission configuration](../apps/controllers/README.md#quick-start).

Use `docker compose logs -f` for logs and `docker compose down` to stop.
**`docker compose down --volumes` deletes saved state.** Add
`-f compose.full.yaml` to those commands for the full stack.
For restart behavior and scaling limits, see [architecture](overview.md#state-and-restart-recovery).

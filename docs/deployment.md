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

**MongoDB + MinIO:**

```sh
docker compose -f compose.full.yaml up --build -d
docker compose -f compose.full.yaml run --build --rm playground
```

Full Compose replaces the database with MongoDB and file storage with MinIO;
the bus still uses FS. Both stacks use the `commander` project name and are
alternatives. See [Mongo](../providers/mongo/README.md) and [S3](../providers/s3/README.md)
for connection details. Bundled credentials are for local development.

Connect another AI client to `http://localhost:4273/mcp` using the
[adapter guide](../apps/adapters/README.md#quick-start). For a temporary session
without Compose, use [Playground's all-in-one mode](../apps/playground/README.md#go-one-executable).

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
controller and MCP. A startup helper sets volume ownership for non-root hosts.
Full Compose uses its MongoDB and MinIO volumes for persistent data instead.

Compose passes `--with-missions` to load the example mission. This upserts supplied
missions without removing others; without the flag the controller loads none.
See [mission configuration](../apps/controllers/README.md#quick-start).

Use `docker compose logs -f` for logs and `docker compose down` to stop.
**`docker compose down --volumes` deletes saved state.** Add
`-f compose.full.yaml` to those commands for the full stack.
For restart behavior and scaling limits, see [architecture](overview.md#state-and-restart-recovery).

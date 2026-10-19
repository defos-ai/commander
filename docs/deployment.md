# Local setup and configuration

Start with [the root quick start](../README.md#quick-start). Compose is a local
development setup; production deployment is not yet documented.

## All-in-one Docker

[Run Playground's all-in-one image](../apps/playground/README.md#docker-recommended)
with `-p 127.0.0.1:4275:4275` before the image name. Completed operation rows link
to their details in Admin at `http://localhost:4275`. Use `/admin`
for the prefilled connection link anytime and `/credentials` for the API login.
For another published port or host, pass `--admin-url` with the browser address
after the image name. Admin reaches the API inside the container;
publish `4274` only if other tools need it. Exiting removes the session's results.

## Local Compose

From the repository root, start the stack and launch Playground:

```sh
docker compose up --build --wait &&
docker compose run --build --rm --no-deps playground
```

Compose runs MCP, the API and admin UI, one controller, two web agents, one browser
agent and one builder agent. Web agents run HTTP checks; the browser renders pages;
the builder checks out the agent template.
It uses RabbitMQ for messaging, MongoDB for the database, and MinIO for file
storage. See [RabbitMQ](../providers/rabbitmq/README.md#quick-start),
[MongoDB](../providers/mongo/README.md), and [S3](../providers/s3/README.md) for
connection details. Bundled infrastructure credentials are for local development.

Connect another AI client to `http://localhost:4273/mcp` with the same bearer token using the
[adapter guide](../apps/adapters/README.md#quick-start). For a temporary session
without Compose, use [Playground's all-in-one mode](../apps/playground/README.md#native-one-executable),
which generates and shares its MCP token automatically.

The API listens on `4274` and admin UI on `4275`. Compose generates an API password
on each container start; set `API_PASSWORD` to preserve your own password. The
[UI quick start](../apps/admin/ui/README.md#quick-start) shows retrieval and port overrides.
Playground's `/admin` link follows the published `COMMANDER_UI_PORT`; set
`COMMANDER_ADMIN_URL` when accessing Admin through another host or proxy.

The one-shot `mcp-credentials` initializer saves a shared MCP token in a named
Docker volume. MCP and Playground mount it read-only and export it to their
process environments. Playground connects automatically, and the token survives
restarts. Set `MCP_TOKEN` before starting Compose to replace it;
`COMMANDER_MCP_TOKEN` overrides it for Playground. Retrieve the running token with
`docker compose exec mcp commander-mcp credentials`; add `--raw` for only the token.
API credentials remain in its process environment.

### Secret encryption

Compose enables secret management automatically. The `secrets-key` initializer
saves one encryption key in its own volume; API and builder mount it read-only and
load it into `SECRETS_ENCRYPTION_KEY`. Restarts reuse the key. Set that variable before
the first start to supply your own 64-character hex key; a later conflicting value
fails startup instead of replacing the saved key.

Back up this key volume with MongoDB and restore them together. Do not delete the
key volume while keeping the database: a new key cannot decrypt existing secrets.
Web/browser agents, MCP, Playground and the controller do not receive this key.
Separately deployed builders need the same key configured explicitly.

## Configuration

### Optional spawner

Use `docker compose --profile spawner up --build --wait` to enable the
[spawner agent](../apps/agents/spawner/README.md#docker). It starts published agent
images as sibling containers on the Compose network. This opt-in service has
Docker daemon access; its children receive only bus/storage bootstrap settings.

Spawned containers outlive the spawner and are not managed by `docker compose
down`. Remove them by their returned container IDs before removing the network.
The `spawner-state` volume retains launch records for retries across restarts.

### Shared settings

| Environment variable | CLI flag | Purpose |
| --- | --- | --- |
| `BUS_URL` | `--bus-url` | Messaging |
| `DATABASE_URL` | `--database-url` | Controller/MCP database |
| `STORAGE_URL` | `--storage-url` | Artifact files and reports |
| `LOG_LEVEL` | `--log-level` | Log verbosity |
| `LOG_FORMAT` | `--log-format` | `text` or `json` logs |
| `MCP_TOKEN` | — | Required standalone MCP bearer token |
| `COMMANDER_MCP_TOKEN` | — | Playground token for an existing MCP server |
| `COMMANDER_ADMIN_URL` | `--admin-url` | Browser-facing Admin URL advertised by Playground |
| `SECRETS_ENCRYPTION_KEY` | — | Encryption key for the API and agents using managed secrets; generated once by Compose |

Flags override environment values. Native hosts default to `./state/bus`,
`./state/database`, and `./state/storage`. Agents need bus and file storage only.
Compose supplies backend settings; application images do not choose providers.
`COMMANDER_MCP_PORT` changes the published Compose port (default `4273`).
Run a host with `--help` for its flags. Action-specific dependencies belong in the
[agent guide](../apps/agents/browser/README.md); model settings belong in
[Playground](../apps/playground/README.md).

Native all-in-one uses its own Admin address; Docker all-in-one defaults to
`http://localhost:4275`; Compose supplies its known published address.
Set `COMMANDER_ADMIN_URL` or pass `--admin-url` to override the link or configure
an existing remote Admin. This advertises the URL; it does not publish a port or
start a remote Admin server. Without it, remote Playground does not guess an
Admin URL or API connection.

## State

Compose creates named Docker volumes; no host `state/` directory is needed.
It persists RabbitMQ, MongoDB, and MinIO data in their own volumes.
Separate volumes hold the shared MCP token and the secrets encryption key.
Builder workspaces have their own persistent volume; stopping or recreating the
builder keeps checked-out source available.
Host inboxes remain transient: broker persistence does not preserve a replaced
host's inbox.

Compose passes `--with-missions` to load both website missions. This upserts supplied
missions without removing others; without the flag the controller loads none.
See [mission configuration](../apps/controllers/README.md#quick-start).

Use `docker compose logs -f` for logs and `docker compose down` to stop.
**`docker compose down --volumes` deletes saved state.**
For restart behavior and scaling limits, see [architecture](overview.md#state-and-restart-recovery).

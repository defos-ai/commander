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

Compose runs MCP, the API and Admin, one controller, and the spawner agent.
Use Playground or another connected MCP client to request additional agents
before starting work that needs their actions. Web agents run HTTP checks,
the browser renders pages, and the builder checks out the agent template.
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
saves one encryption key in its own volume; API and spawner mount it read-only and
load it into `SECRETS_ENCRYPTION_KEY`. Restarts reuse the key. Set that variable before
the first start to supply your own 64-character hex key; a later conflicting value
fails startup instead of replacing the saved key.

Back up this key volume with MongoDB and restore them together. Do not delete the
key volume while keeping the database: a new key cannot decrypt existing secrets.
Spawned children, MCP, Playground and the controller do not receive this key.
Builders need the same key configured explicitly only for actions that use
managed secrets; `checkout-template` does not need it.

## Configuration

### Spawned agents

The [spawner](../apps/agents/spawner/README.md#docker) uses the host Docker socket
to start released images as sibling containers on the Compose network. It starts
without child agents. Through chat or MCP, request `spawn-agent` with an exact
package reference, such as `github.com/defos-ai/commander-web-agent@v0.2.0`,
then start the mission after the new agent is ready. See the
[runtime spawning example](../apps/agents/spawner/README.md#during-a-mission).
Launching packages needs Internet access for GitHub manifests and uncached image
pulls. Children receive bus/storage settings, without the socket or secrets
encryption key.

`docker compose restart spawner-agent` reuses live children and their saved launch
records in `spawner-state`. Spawned containers have no automatic restart policy:
stopped or missing children require a new launch, not a retry of their old record.
See [stopping and resetting agents](#stopping-and-resetting-agents) before teardown.

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
[browser agent guide](https://github.com/defos-ai/commander-browser-agent); model settings belong in
[Playground](../apps/playground/README.md).

Native all-in-one uses its own Admin address; Docker all-in-one and standalone
Playground default their links to `http://localhost:4275`. Compose supplies its
known published address. Set `COMMANDER_ADMIN_URL` or pass `--admin-url` to
override the link. Standalone Playground does not start Admin; the link does not
publish a port or infer an API connection from MCP.

## State

Compose creates named Docker volumes; no host `state/` directory is needed.
It persists RabbitMQ, MongoDB, and MinIO data in their own volumes.
Separate volumes hold the shared MCP token and the secrets encryption key.
The `spawner-state` volume retains launch records. Each spawned builder owns its
workspaces; Docker package workspaces live inside that builder's container.
Host inboxes remain transient: broker persistence does not preserve a replaced
host's inbox.

Compose passes `--with-missions` to load both website missions. This upserts supplied
missions without removing others; without the flag the controller loads none.
See [mission configuration](../apps/controllers/README.md#quick-start).

Use `docker compose logs -f` for service logs. Spawned agents have their own
containers: use `docker logs <container-id>` for those.

### Stopping and resetting agents

Spawned containers are not Compose services: `docker compose down` does not stop
them. To stop the default deployment and reset only agent launch records:

```sh
docker compose stop spawner-agent
for container in $(docker ps -aq --filter network=commander_default --filter label=ai.defos.commander.spawn-owner); do docker rm -f "$container"; done
docker compose rm -f spawner-agent
docker volume rm commander_spawner-state
docker compose down
```

For a custom Compose project name, replace `commander_default` and
`commander_spawner-state` with that project's network and volume names. Removing
launch records is deliberate here, after the spawner and its children stop; it
lets later spawn requests create fresh agents. MongoDB, artifacts and credentials
remain; removing a spawned builder's container also removes its workspaces.
**`docker compose down --volumes` deletes the Compose stack's saved state.**
For restart behavior and scaling limits, see [architecture](overview.md#state-and-restart-recovery).

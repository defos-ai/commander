# Deploy Commander

Choose [Containers](#containers) for individual hosts,
[Compose](#local-compose) for a local stack, or [Kubernetes](#kubernetes) for
cluster deployment. For native binaries, background hosts and coding-agent
connections, follow [installation](installation.md).

## Containers

Run the all-in-one image for Playground and local hosts, or deploy
[separate component images](#separate-services) for your infrastructure.

### All-in-one Docker

Run a disposable container:

```sh
docker run --rm -it --name commander -p 127.0.0.1:4275:4275 ghcr.io/defos-ai/commander-aio:stable
```

Keep saved state across container removal with a named volume:

```sh
docker run --rm -it --name commander -v commander-state:/home/commander/.commander -p 127.0.0.1:4275:4275 ghcr.io/defos-ai/commander-aio:stable
```

Open [Admin](http://localhost:4275), or use `/admin` in Playground.
Use `/credentials` for the API login. The named volume keeps results, credentials
and chat history after exit; without it, `--rm` removes the container's saved state.
Choose a model and enter its API key in Playground, then ask:

> Create a Hacker News digest

For another host or published port, pass `--admin-url=<browser-facing URL>` after
the image name. Publish API port `4274` only if another tool needs it.

### Separate services

Run hosts independently when they need different infrastructure or lifecycle.
Replace `stable` with one shared version tag to pin your deployment:

| Component | Image | Deployment guide |
| --- | --- | --- |
| HTTP API | `ghcr.io/defos-ai/commander-api:stable` | [API hosting and configuration](../apps/adapters/api/README.md) |
| MCP | `ghcr.io/defos-ai/commander-mcp:stable` | [MCP hosting and authentication](../apps/adapters/mcp/README.md) |
| Admin UI | `ghcr.io/defos-ai/commander-admin:stable` | [Standalone Admin hosting](../apps/admin/ui/README.md#standalone-docker-image) |
| Controller | `ghcr.io/defos-ai/commander-controller:stable` | [Controller setup](../apps/controllers/README.md#quick-start) |
| Spawner | `ghcr.io/defos-ai/commander-spawner-agent:stable` | [Native, Docker and Kubernetes runtimes](../apps/agents/spawner/README.md) |
| Builder | `ghcr.io/defos-ai/commander-builder-agent:stable` | [Builder setup](../apps/agents/builder/README.md) |

Published images target `linux/amd64`; use compatible hosts and Kubernetes nodes.

Connect hosts to the same configured providers and run one active controller per
database. API and controller need the same secret encryption key. See
[secret encryption](#secret-encryption) for backup and restore requirements.

## Local Compose

From the repository root:

```sh
docker compose up --build --wait
docker compose run --build --rm --no-deps playground
```

The first command starts the stack in the background. The second opens the
optional Playground client; exiting it leaves the services running.

Spawner is ready to use. Ask chat to install other agents when needed.
Compose supplies RabbitMQ, MongoDB and MinIO, using local development credentials.
Admin adds bundled missions through the API at startup, preserving existing names.

| Connect to… | Address | Get credentials |
| --- | --- | --- |
| Admin | <http://localhost:4275> | `docker compose exec api commander-api credentials` |
| HTTP API | <http://localhost:4274> | Same API login |
| MCP | <http://localhost:4273/mcp> | `docker compose exec mcp commander-mcp credentials` |

Playground receives the MCP token and local API login automatically, including
for `/admin` commands and secret forms. Sign in to browser Admin separately.
Its `playground-state` volume retains client settings, chat history and locally
remembered provider keys between `docker compose run --rm` sessions.
To connect another MCP client, follow [the MCP guide](../apps/adapters/README.md#quick-start).

Compose saves the API password and MCP token in separate volumes and reuses them
across restarts. Set `API_PASSWORD` or `MCP_TOKEN` before launch to supply your own.
Add `--raw` to a credentials command for just its password or token.

After changing `API_PASSWORD`, rerun initialization and recreate the API:

```sh
docker compose up --force-recreate --wait api
docker compose run --rm --no-deps playground
```

Close any existing Playground session first. For a custom `COMMANDER_API_URL`,
also supply that API's `COMMANDER_API_USERNAME` and `COMMANDER_API_PASSWORD`, or
enter its login in Playground's local form.

### Optional builder

Start the builder when you need to build agents:

```sh
docker compose --profile builder up --build --wait
```

The builder keeps its source checkouts in `builder-state`. See
[Builder setup](../apps/agents/builder/README.md) for native use and managed secrets.

### Secret encryption

Compose generates and saves `SECRETS_ENCRYPTION_KEY` automatically. To supply your
own, set a 64-character hex value before the first start. A conflicting value on
later starts is rejected.

The controller mounts the saved key read-only and decrypts requested secrets
using the same key as the API. Agents receive values through the broker.

**Back up the `secrets` volume with MongoDB and restore them together.** A new
key cannot decrypt existing secrets. All-in-one retains its key with its saved
local state; back up the entire profile. `--temporary` uses a disposable key.
For a separate controller, configure the same key as the API. Agents do not need
that key for managed-secret delivery. See [secret setup](../framework/secrets/README.md).

### Debugging

Use the [VS Code debugging guide](../.vscode/debug/README.md) to attach to
the controller, API and MCP containers.

## Kubernetes

Deploy the [component images](#separate-services) through your Kubernetes
manifests. API, MCP, controller and agents need reachable provider addresses;
use RabbitMQ, MongoDB and S3-compatible storage with persistent data.

1. Configure shared `BUS_URL`, `DATABASE_URL` and `STORAGE_URL` where required.
   Give API and controller the same `SECRETS_ENCRYPTION_KEY`, keeping it out of
   agent configuration. Follow [secret backup requirements](#secret-encryption).
2. Run one active controller per database. Use `strategy.type: Recreate` for its
   Deployment so upgrades do not briefly run overlapping controllers.
3. Run Spawner in the cluster with `SPAWNER_RUNTIME=kubernetes`. Its own
   ServiceAccount needs namespaced Pod `get`, `create` and `patch` permissions.
   Create the child namespace, ServiceAccount, ConfigMap and Secret, then set
   their references using the [Spawner Kubernetes settings](../apps/agents/spawner/README.md#kubernetes).
   The child Secret supplies `BUS_URL` and `STORAGE_URL` for the same providers;
   child Pods do not mount a Kubernetes API token.
4. Expose API, MCP and Admin through Services and your Ingress. Configure public
   hostnames and proxy settings through their component guides. Use
   `UI_API_UPSTREAMS` for Admin's internal API mapping; see
   [Admin connections](../apps/admin/ui/README.md#connections).

Run the official Builder on a separate native or Docker host connected to the
same broker and storage; Spawner's Kubernetes runtime does not support it yet.
Spawned Pods survive Spawner shutdown. Follow the
[agent shutdown guidance](../apps/agents/spawner/README.md#stop-or-replace-agents)
before removing or replacing them.

## Configuration

### Spawned agents

Ask your MCP client or Playground to install an agent from a trusted GitHub
repository or choose one from the [agent sources](../sources/README.md). In Compose,
the [spawner](../apps/agents/spawner/README.md#docker) uses the host Docker socket to
launch sibling containers on the Compose network. Installation needs Internet access.

The spawner can request one or multiple instances. Check the returned agents'
readiness before starting dependent work. After an uncertain launch, inspect the
existing container before trying again.
Spawned containers keep running when the spawner restarts and are
[removed separately during teardown](#stopping-and-resetting-agents).

### Shared settings

Set these in your shell or an untracked `.env` before running Compose:

| Setting | Use it to… |
| --- | --- |
| `COMMANDER_MCP_PORT`, `COMMANDER_API_PORT`, `COMMANDER_UI_PORT` | Change published ports (`4273`, `4274`, `4275`) |
| `COMMANDER_ADMIN_URL` | Set the browser-facing Admin link for a remote host or proxy |
| `BUS_URL`, `DATABASE_URL`, `STORAGE_URL` | Connect hosts to different providers |
| `LOG_LEVEL`, `LOG_FORMAT` | Choose verbosity and `text` or `json` logs |
| `TASK_TIMEOUT` | Set the controller's initial per-task result wait; default `15m`, for example `30m` for builds |

Standalone hosts accept matching flags such as `--bus-url`; flags override
environment values. Run `<executable> --help` for its options.
Use the component guides for [API](../apps/adapters/api/README.md),
[Admin hosting](../apps/admin/ui/README.md#connections),
[spawner](../apps/agents/spawner/README.md) and
[model settings](../apps/playground/README.md).

## State

Compose keeps data in named Docker volumes:

| Volume | Keep it for… |
| --- | --- |
| `api-credentials` | The shared local API password |
| `mcp-credentials` | The shared MCP token |
| `builder-state` | The optional builder's source checkouts |
| `playground-state` | Client settings, chat history and locally remembered provider keys |
| `secrets` | The key needed to decrypt saved secrets |
| `mongo` | Missions, operations, results, encrypted secrets and execution checkpoints |
| `minio` | Artifact files and reports |
| `rabbitmq` | Broker data |

Use `docker compose logs -f` for services and `docker logs <container-id>` for
spawned agents.

### Import missions

With the stack running, import a mission file from your host:

```sh
docker compose run --rm --no-deps -v "$PWD/mission.json:/mission.json:ro" missions missions import /mission.json
```

Existing mission names are preserved. Native Playground can use
`/admin missions import FILE`; paths refer to Playground's filesystem.
See [Admin mission commands](../apps/admin/README.md#missions) for validation and export.

### Stopping and resetting agents

Spawned containers are not Compose services. Stop the spawner, remove its children,
then stop the stack:

```sh
docker compose stop spawner-agent
for container in $(docker ps -aq --filter network=commander_default --filter label=ai.defos.commander.spawn-owner); do docker rm -f "$container"; done
docker compose down
```

For a custom Compose project, replace `commander_default` with its network name.
Saved volumes remain. Removing a spawned builder also removes that container's
workspaces. **`docker compose down --volumes` deletes the stack's saved state.**

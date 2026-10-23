# Local setup and configuration

For the fastest start, use the [Docker or binary quick start](../README.md#quick-start).
Use Compose to develop against persistent local services.

## All-in-one Docker

```sh
docker run --rm -it --name commander -p 127.0.0.1:4275:4275 ghcr.io/defos-ai/commander-aio:stable
```

Open [Admin](http://localhost:4275), or use `/admin` in Playground.
Use `/credentials` for the API login. Exiting deletes the session's results.

For another host or published port, pass `--admin-url=<browser-facing URL>` after
the image name. Publish API port `4274` only if another tool needs it.

## Local Compose

From the repository root:

```sh
docker compose up --build --wait && docker compose run --build --rm --no-deps playground
```

Builder and spawner are ready to use. Ask chat to install other agents when needed.
Compose supplies RabbitMQ, MongoDB and MinIO, using local development credentials.

| Connect to… | Address | Get credentials |
| --- | --- | --- |
| Admin | <http://localhost:4275> | `docker compose exec api commander-api credentials` |
| HTTP API | <http://localhost:4274> | Same API login |
| MCP | <http://localhost:4273/mcp> | `docker compose exec mcp commander-mcp credentials` |

Playground receives the MCP token and local API login automatically, including
for `/admin` commands and secret forms. Sign in to browser Admin separately.
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

### Secret encryption

Compose generates and saves `SECRETS_ENCRYPTION_KEY` automatically. To supply your
own, set a 64-character hex value before the first start. A conflicting value on
later starts is rejected.

**Back up the `secrets-key` volume with MongoDB and restore them together.** A new
key cannot decrypt existing secrets. All-in-one generates a temporary session key.
For separately deployed agents using managed secrets, configure the same key as
the API. See [secret setup](../framework/secrets/README.md).

## Configuration

### Spawned agents

Ask Playground to install an agent from a trusted GitHub repository or choose one
from the [catalog](../catalog/README.md). The
[spawner](../apps/agents/spawner/README.md#docker) uses the host Docker socket to
launch sibling containers on the Compose network. Installation needs Internet access.

Each installation creates one instance. Check readiness before starting dependent
work. After an uncertain launch, inspect the existing container before trying again.
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

Standalone hosts accept matching flags such as `--bus-url`; flags override
environment values. Run `<executable> --help` for its options.
Use the component guides for [API](../apps/adapters/api/README.md),
[Admin hosting](../apps/admin/ui/README.md#connections-and-sessions),
[spawner](../apps/agents/spawner/README.md) and
[model settings](../apps/playground/README.md).

## State

Compose keeps data in named Docker volumes:

| Volume | Keep it for… |
| --- | --- |
| `mongo` | Missions, operations, results, encrypted secrets and execution checkpoints |
| `minio` | Artifact files and reports |
| `rabbitmq` | Broker data |
| `builder-workspaces` | The default builder's source checkouts |
| `secrets-key` | The key needed to decrypt saved secrets |
| `api-credentials` | The shared local API password |
| `mcp-credentials` | The shared MCP token |

Use `docker compose logs -f` for services and `docker logs <container-id>` for
spawned agents. To load your own missions, see [mission setup](../apps/controllers/README.md#quick-start).

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

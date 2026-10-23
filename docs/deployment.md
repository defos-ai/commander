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

Playground receives the MCP token automatically. To connect another client, follow
[the MCP guide](../apps/adapters/README.md#quick-start).

Set `API_PASSWORD` before launch to keep a fixed API password across restarts.
The MCP token is saved in a volume; set `MCP_TOKEN` to supply your own.
Add `--raw` to a credentials command for just its password or token.

### Secret encryption

Compose generates and saves `SECRETS_ENCRYPTION_KEY` automatically. To supply your
own, set a 64-character hex value before the first start. A conflicting value on
later starts is rejected.

**Back up the `secrets-key` volume with MongoDB and restore them together.** A new
key cannot decrypt existing secrets. All-in-one generates a temporary session key.
For separately deployed agents using managed secrets, configure the same key as
the API. See [secret setup](../framework/secrets/README.md).

### Debug Compose services in VS Code

Open the repository root in VS Code and install the **Go** extension (`golang.go`).
Start the debug stack from the repository root:

```sh
docker compose -f compose.yaml -f compose.debug.yaml up --build --wait
```

[The debug override](../compose.debug.yaml) replaces the controller, API and MCP
with separate `:debug` images built by [Dockerfile.debug](../Dockerfile.debug).
Builder, spawner, infrastructure, Admin UI and Playground use their normal images.
The override uses the same Compose project and saved
volumes as normal development; it replaces the existing services, rather than
starting a second controller against their database.

Each Go host starts under Delve immediately, so health checks and dependent
services can become ready before VS Code connects. Select a **Commander Docker:**
configuration in **Run and Debug**, set breakpoints and press **F5**. Choose
**Commander Docker: All Services** to attach to all three processes.
The native controller launch and local MCP attach configurations remain available.

| Service | VS Code configuration | Local debugger port |
| --- | --- | --- |
| `controller` | Commander Docker: Controller | `2345` |
| `api` | Commander Docker: API | `2346` |
| `mcp` | Commander Docker: MCP | `2347` |

All debugger ports bind to `127.0.0.1`. These development containers enable
ptrace and disable the seccomp filter for Delve; use the normal Compose file for
regular deployment. Build for the host's native architecture (including arm64
on Apple Silicon), since debugging an emulated container may not work.

Debug binaries disable optimization and inlining and retain `/src` source paths.
[The attach configurations](../.vscode/launch.json) map the local repository to
`/src`. If a breakpoint stays hollow, confirm the correct service is attached and
rebuild its image after source edits. Each process needs its own breakpoints;
stepping does not follow a broker message into another service. While paused,
requests can time out, health checks can fail and agent leases can expire.

For Playground, keep the override in the command so Compose uses the debug images:

```sh
docker compose -f compose.yaml -f compose.debug.yaml run --build --rm --no-deps playground
```

API password generation, the secrets encryption key, the shared MCP token volume
and explicit credential overrides work as usual. In debug containers Delve occupies PID 1, so the normal
credential commands cannot discover generated credentials automatically. These
explicit lookups import Delve's startup environment in a shell before calling the
credential command; they do not save credentials to files or pass them as arguments:

```sh
docker compose exec api bash -ec 'while IFS= read -r -d "" entry; do export "$entry"; done < /proc/1/environ; exec commander-api credentials'
docker compose exec mcp bash -ec 'while IFS= read -r -d "" entry; do export "$entry"; done < /proc/1/environ; exec commander-mcp credentials'
```

To rebuild a changed service, disconnect its VS Code session, run the following
command (replace `controller` as needed), then attach again:

```sh
docker compose -f compose.yaml -f compose.debug.yaml up -d --build --no-deps controller
```

**Disconnect** leaves a running target available for another attach; continue a paused target
before disconnecting. **Terminate** stops the target and Delve, and the override
disables automatic restarts; start that service again with the command above.
To debug startup, temporarily remove `--continue` from that service's Delve
arguments, start it with `up -d --no-deps SERVICE` (without `--wait`), then attach
before continuing. Start its dependencies first using the normal debug command.

Return to normal images, retaining saved volumes:

```sh
docker compose -f compose.yaml up --build --wait
```

See the [VS Code Go remote debugging guide](https://github.com/golang/vscode-go/blob/master/docs/debugging.md#remote-debugging)
for attach and source mapping details.

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

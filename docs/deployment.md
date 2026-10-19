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

Compose runs MCP, the API and admin UI, one controller, two web agents and one
browser agent. Web agents run HTTP checks; the browser agent runs rendering checks.
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

### Debug Compose services in VS Code

Open the repository root in VS Code and install the **Go** extension (`golang.go`).
Start the debug stack from the repository root:

```sh
docker compose -f compose.yaml -f compose.debug.yaml up --build --wait
```

[The debug override](../compose.debug.yaml) replaces the controller, API, MCP,
browser agent and both web agents with separate `:debug` images built by
[Dockerfile.debug](../Dockerfile.debug). Infrastructure, Admin UI and Playground
use their normal images. The override uses the same Compose project and saved
volumes as normal development; it replaces the existing services, rather than
starting a second controller against their database.

Each Go host starts under Delve immediately, so health checks and dependent
services can become ready before VS Code connects. Select a **Commander Docker:**
configuration in **Run and Debug**, set breakpoints and press **F5**. Choose
**Commander Docker: All Services** to attach to all six processes, or
**Commander Docker: Both Web Agents** because either instance can receive work.
The existing native launch configurations remain available.

| Service | VS Code configuration | Local debugger port |
| --- | --- | --- |
| `controller` | Commander Docker: Controller | `2345` |
| `api` | Commander Docker: API | `2346` |
| `mcp` | Commander Docker: MCP | `2347` |
| `browser-agent` | Commander Docker: Browser Agent | `2348` |
| `web-agent-1` | Commander Docker: Web Agent 1 | `2349` |
| `web-agent-2` | Commander Docker: Web Agent 2 | `2350` |

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

API password generation, the shared MCP token volume and explicit credential
overrides work as usual. In debug containers Delve occupies PID 1, so the normal
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

Rebuild both web agent services when changing their shared code. **Disconnect**
leaves a running target available for another attach; continue a paused target
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
It persists RabbitMQ, MongoDB, and MinIO data in their own volumes;
the only shared application volume holds the MCP credential, separate from data.
Host inboxes remain transient: broker persistence does not preserve a replaced
host's inbox.

Compose passes `--with-missions` to load both website missions. This upserts supplied
missions without removing others; without the flag the controller loads none.
See [mission configuration](../apps/controllers/README.md#quick-start).

Use `docker compose logs -f` for logs and `docker compose down` to stop.
**`docker compose down --volumes` deletes saved state.**
For restart behavior and scaling limits, see [architecture](overview.md#state-and-restart-recovery).

# Spawner

The spawner starts published agents as native processes or Docker containers.
It executes the controller-dispatched `spawn-agent` action.

Any compatible GitHub release package can be installed, including packages
outside Commander's discovery catalog. Read the spawner's registered
responsibilities through `list_agents` or `describe_agent` for installation
guidance, and `describe_action` for the `spawn-agent` input schema.
MCP's `get_agent_catalog` discovers available agents. Pass an entry's package
and visibility, or a GitHub URL, to the spawner. An omitted version or `@stable`
selects the repository's promoted Latest release; an explicit `@v0.2.0` pins it.

## Run

From the repository root, using the same bus and artifact storage as your controller:

```sh
go run ./apps/agents/spawner/cmd --bus-url ./state/bus --storage-url ./state/storage
```

Each host gets a fresh `spawner-agent-<suffix>` ID. To supply its suffix, use
`--agent-id-suffix=1234abcd` or `COMMANDER_AGENT_ID_SUFFIX`; values must be exactly
eight lowercase hex characters. The flag overrides the environment, and an
explicit `--agent-id-suffix=` generates a fresh ID. Child IDs are assigned separately.

Install packages through Playground or the [`install-agent` mission](#during-a-mission).
A bare GitHub URL or `@stable` works; the spawner resolves and saves the version
before downloading. Native packages must contain `agent.yaml` and an executable
for your OS and architecture.

For agents in a repository's Go module, include the module path. The next
Commander release will provide this spawner package:

```text
github.com/defos-ai/commander/apps/agents/spawner@v0.2.0
```

It resolves `agent.yaml` from the `apps/agents/spawner/v0.2.0` release in the
Commander repository, with six native platforms and a Docker image. The manifest
version is `v0.2.0`. These package assets become available only when that release
publishes; existing releases are unchanged. See
[package resolution](../../../docs/agent-packages.md#resolving-a-package).

The next Commander release will include `commander-spawner-agent_<os>_<arch>`
executables for Linux, macOS and Windows on amd64/arm64 (`.exe` for Windows).
After downloading one as `commander-spawner-agent` and making it executable on
Linux/macOS, run it with the same settings:

```sh
./commander-spawner-agent --bus-url ./state/bus --storage-url ./state/storage
```

On Windows, use `.\commander-spawner-agent.exe`. Until that release is published,
use the source command above. See [release artifacts](../../../docs/releases.md#native-binaries).

### Docker

Start Compose with its default spawner:

```sh
docker compose up --build --wait
```

Compose starts builder and spawner agents. Ask in Playground to install the web
agent, or use the `install-agent` mission through MCP. Requested agents run as sibling
containers on Commander's network, sharing RabbitMQ and S3.

The package must publish a `docker.image` digest in `agent.yaml`. Standalone
hosts set `SPAWNER_RUNTIME=docker` and `SPAWNER_DOCKER_NETWORK=<network>` with
reachable AMQP/S3 settings. Docker must be running Linux containers. The spawner
uses the Docker Engine API; no Docker CLI is needed inside its image.

The next Commander release also publishes
`ghcr.io/defos-ai/commander-spawner-agent:<version>` and promotes `:stable` only
after the complete release succeeds. The image defaults to native mode; Compose
sets Docker mode and supplies its network and socket access. Until that release
is published, Compose builds the image locally.

A spawner started through its release package also defaults to native mode.
It receives no automatic Docker privileges or socket access, and package spawning
does not install prerequisites for the agents it later starts.

The Compose spawner has control of the host Docker daemon through its socket.
Spawned agents receive no Docker socket, host mounts, published ports or elevated
capabilities. Package files cannot request these privileges.

### During a mission

For runtime spawning, read the `install-agent` mission and `spawn-agent` action
schemas through MCP. Start an `install-agent` operation, then assign its
`spawn-agent` task with the same input:

```json
{"package":"github.com/defos-ai/commander-example-agent@v0.2.0","visibility":"public"}
```

It verifies the package, assigns an agent ID, starts the instance and waits for
that exact ID and type to announce themselves. Only the eight-character hex suffix
is passed as `COMMANDER_AGENT_ID_SUFFIX`; each agent adds its own fixed type.
The result includes `runtime`, the PID or container ID, and action readiness. Missing requirements do not
prevent registration; the controller still determines when an agent is eligible
for work.

Native children stop during spawner shutdown. Linux also stops their process
group; other platforms stop the direct child only. Docker containers keep running.
Use a separate provisioning operation before a mission that needs the new actions.
Review the spawn result, resolve its model continuation with no follow-up actions,
and complete provisioning. Recheck agent readiness and the target mission's
preflight before assigning dependent work.

Use packages from authors you trust. A checksum verifies downloaded bytes against
the manifest; it does not establish whether the program is safe to run.

### Private GitHub packages

Pass the package reference or GitHub URL and `"visibility":"private"` from the catalog
or operator to the install mission and `spawn-agent`. The spawner does not load
the catalog. It uses its environment `GITHUB_TOKEN` first; otherwise it requests
the managed `GITHUB_TOKEN` secret for `spawner-agent`. Supply the value through
the Playground secret form or Admin. The same task waits and resumes when the
token arrives, within its existing execution timeout. Never paste a token into chat.

Compose forwards an optional host `GITHUB_TOKEN` to the spawner only. Native and
Docker AIO can receive it through their environment too. Public or omitted
visibility uses anonymous downloads and image pulls, even when a token is configured.

In Docker mode, private installs reuse that token for image pulls from exactly
`ghcr.io`. The token needs repository access and access to the image; for a
personal access token, GitHub requires a classic token with `read:packages`.
See [GitHub's registry authentication guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry#authenticating-to-the-container-registry).
Other registries remain anonymous. The spawner does not read host Docker login
credentials, save tokens in launch records or pass them to spawned agents.

## Current scope

The action supports public and private GitHub packages, including stable resolution.
Private Docker image authentication is supported for GHCR.
Action inputs accept GitHub URLs,
`@stable`, and exact versions. A missing version means stable.
`SPAWNER_RUNTIME` defaults to `native`, including in its Dockerfile;
the Compose service explicitly selects Docker mode.

Compose and all-in-one configure spawner's managed secret exchange with the same
encryption key as the API. For a standalone spawner, set `SECRETS_ENCRYPTION_KEY`
to that API's 64-character hex key. An invalid key prevents startup; leaving it
unset disables managed secret exchange.

The spawner also supplies this key to the official builder package:
`github.com/defos-ai/commander/apps/agents/builder@<version>`, after resolving an
exact published version and verifying its `builder-agent` manifest type.
Other packages receive no key, even if they declare the same type or appear in
the catalog. This trusts published versions of that exact Commander module.
The key stays out of launch records, results and logs. Docker administrators can
read it from the builder container's environment.

[Playground all-in-one](../../playground/README.md#installing-agents) embeds the
spawner in native mode alongside the built-in builder, including inside Docker
AIO. Install additional agents through chat/MCP.
Its state, downloads and child working directories are temporary; shutdown awaits child
processes before removing them.

`BUS_URL` and `STORAGE_URL` configure the shared providers. Children receive those
settings and explicit process/S3 settings, without inheriting API, MCP or model
credentials. S3 credentials must be available as environment settings; host profile
files are not shared. Native processes run as the spawner's OS user, not in a sandbox.

Native children also receive the deployment's `CHROMIUM_PATH` and
`CHROMIUM_NO_SANDBOX` settings when set. These let browser agents use the selected
Chromium executable and the container's sandbox configuration.

The `spawn-agent` action saves its package selection and launch intent as
controller-owned execution checkpoints in FS or MongoDB. It waits for persistence
before downloading the package or creating the agent. Retries reuse the same
version even if Latest changes; a new execution can select the newer release.
Checkpoint failures stop the action.

Native downloads and child working directories use `./state/spawner`;
`SPAWNER_STATE_DIR` changes that location in native mode. Docker mode needs no
local launch state or state volume.
Native retries reuse the live child in the same host. After a native host restart,
existing launch intents report uncertainty; inspect old processes before requesting
replacements through a new task.

Docker retries verify the saved ownership labels, image, network and bootstrap
configuration, then await registration. A restored manager can observe a running
container. Only the manager that received confirmation
of the new intent may start a container still in `created` state; another manager reports
uncertainty. Checkpoints do not automatically replay tasks after a host restart.
Builder retries also verify the encryption key; changing or removing it requires
a fresh launch rather than silently reusing a differently configured container.
Missing or stopped containers are never recreated or restarted by a retry;
request a replacement with a new task. Containers use no automatic
restart policy and keep running when the spawner restarts.
They are separate containers, not Compose services; follow the
[scoped teardown](../../../docs/deployment.md#stopping-and-resetting-agents) to
remove children before `docker compose down`. Retain controller execution
records while these containers exist.

Registration waits default to 30 seconds for native mode and 45 seconds for
Docker (`--registration-timeout`). Retrying the same launch request observes its
existing intent; creating a new task is a new launch request. Docker pulls have a
ten-minute deadline.

Downloads use HTTPS to GitHub release hosts without environment proxies. Private
downloads send their token only to GitHub's API; redirects carry no credentials.
Manifests are limited to 64 KiB and native executables to
512 MiB. The native host removes its downloads after child shutdown and retains
private child working directories.

See the [package contract](../../../docs/agent-packages.md) and
[implementation guide](AGENTS.md).

## Development

[agent.go](agent.go) defines `AgentType`, `NewAgent` and the action catalog.
Action implementations live in `actions/`; `cli/` owns configuration and providers.

From the repository root with Go 1.26.7+:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/spawner/...
go vet ./apps/agents/spawner/...
```

These focused tests use local fixtures for package validation, uncertain launches
and cleanup. They need no GitHub credentials, Docker or external services.

The [API/MCP scenarios](../../../framework/tests/README.md#mcp-api-tests) cover
spawning, controller registration, action execution and artifact retrieval with
real providers. They use a local release endpoint and Docker registry.

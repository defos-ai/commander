# Spawner

The spawner starts published agents as native processes or Docker containers.
It exposes `spawn-agent` and can also start packages on launch.

## Run

From the repository root, using the same bus and artifact storage as your controller:

```sh
go run ./apps/agents/spawner/cmd --bus-url ./state/bus --storage-url ./state/storage
```

To start a published package immediately, add:

```sh
--spawn-agents github.com/defos-ai/commander-example-agent@v0.2.0
```

Repeat the flag for multiple instances. The release must contain `agent.yaml`
and a native executable for your OS and architecture.

The next Commander release will include `commander-spawner-agent_<os>_<arch>`
executables for Linux, macOS and Windows on amd64/arm64 (`.exe` for Windows).
After downloading one as `commander-spawner-agent` and making it executable on
Linux/macOS, run it with the same settings:

```sh
./commander-spawner-agent --bus-url ./state/bus --storage-url ./state/storage \
  --spawn-agents github.com/defos-ai/commander-example-agent@v0.2.0
```

On Windows, use `.\commander-spawner-agent.exe`. Until that release is published,
use the source command above. See [release artifacts](../../../docs/releases.md#native-binaries).

### Docker

Start Compose with its default spawner:

```sh
docker compose up --build --wait
```

It launches two web agents and one browser agent from their `v0.2.0` packages as
sibling containers on Commander's network, sharing RabbitMQ and S3. To replace
that list, change `command` under `spawner-agent` in `compose.yaml`:

```yaml
command: ["--spawn-agents", "github.com/defos-ai/commander-example-agent@v0.2.0"]
```

For an existing deployment, [remove its children and reset launch records](../../../docs/deployment.md#stopping-and-resetting-agents)
before changing startup entries. Existing slots cannot switch packages.

The package must publish a `docker.image` digest in `agent.yaml`. Standalone
hosts set `SPAWNER_RUNTIME=docker` and `SPAWNER_DOCKER_NETWORK=<network>` with
reachable AMQP/S3 settings. Docker must be running Linux containers. The spawner
uses the Docker Engine API; no Docker CLI is needed inside its image.

The next Commander release also publishes
`ghcr.io/defos-ai/commander-spawner-agent:<version>` and promotes `:stable` only
after the complete release succeeds. The image defaults to native mode; Compose
sets Docker mode and supplies its network, socket access and persistent launch
state. Until that release is published, Compose builds the image locally.

The Compose spawner has control of the host Docker daemon through its socket.
Spawned agents receive no Docker socket, host mounts, published ports or elevated
capabilities. Package files cannot request these privileges.

### During a mission

For runtime spawning, have the controller dispatch `spawn-agent` with:

```json
{"package":"github.com/defos-ai/commander-example-agent@v0.2.0"}
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

Use packages from authors you trust. A checksum verifies downloaded bytes against
the manifest; it does not establish whether the program is safe to run.

## Current scope

The host supports public GitHub releases and public Docker images. Private
packages and moving tags such as `stable` are not implemented.
`SPAWNER_RUNTIME` defaults to `native`, including in its Dockerfile;
the Compose service explicitly selects Docker mode.

[Playground all-in-one](../../playground/README.md#startup-agents) embeds the
spawner in native mode, including inside Docker AIO. It downloads web/browser
`v0.2.0` packages by default and accepts `--spawn-agents` overrides. Its state,
downloads and child working directories are temporary; shutdown awaits child
processes before removing them.

`BUS_URL` and `STORAGE_URL` configure the shared providers. Children receive those
settings and explicit process/S3 settings, without inheriting API, MCP or model
credentials. S3 credentials must be available as environment settings; host profile
files are not shared. Native processes run as the spawner's OS user, not in a sandbox.

Native children also receive the deployment's `CHROMIUM_PATH` and
`CHROMIUM_NO_SANDBOX` settings when set. These let browser agents use the selected
Chromium executable and the container's sandbox configuration.

Launch records live in `./state/spawner`; set `SPAWNER_STATE_DIR` to change it.
Native retries reuse the live child in the same host. After a native host restart,
existing requests report uncertainty; inspect old processes before requesting
replacements with a new state directory.

Docker retries also work after a spawner restart: they verify the saved ownership
labels, image, network and bootstrap configuration, then await registration.
Missing or stopped containers are never recreated or restarted by a retry;
request a replacement with a new task or startup slot. Containers use no automatic
restart policy. `docker compose restart spawner-agent` reuses live children.
They are separate containers, not Compose services; follow the
[scoped teardown](../../../docs/deployment.md#stopping-and-resetting-agents) to
remove children and reset launch records before `docker compose down`. Keep
`spawner-state` while these containers exist. Only one Docker spawner may
use a state directory at a time; a second host fails startup.

Registration waits default to 30 seconds for native mode and 45 seconds for
Docker (`--registration-timeout`). A timed-out or canceled wait can be retried
without launching a duplicate. Docker pulls have a ten-minute deadline.

Downloads use HTTPS to GitHub release hosts without environment proxies or
deployment credentials. Manifests are limited to 64 KiB and native executables to
512 MiB. The native host removes its downloads after child shutdown and retains
launch records and private child working directories.

See the [package contract](../../../docs/agent-packages.md) and
[implementation guide](AGENTS.md).

## Development

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

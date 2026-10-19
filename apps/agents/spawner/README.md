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
--spawn-agents github.com/defos-ai/commander-example-agent@v0.1.0
```

Repeat the flag for multiple instances. The release must contain `agent.yaml`
and a native executable for your OS and architecture.

### Docker

Enable the optional Compose spawner:

```sh
docker compose --profile spawner up --build --wait
```

It launches sibling containers on Commander's network with the shared RabbitMQ
and S3 settings. To start a package with the service, add this under
`spawner-agent` in `compose.yaml`, then run the command above:

```yaml
command: ["--spawn-agents", "github.com/defos-ai/commander-example-agent@v0.1.0"]
```

The package must publish a `docker.image` digest in `agent.yaml`. Standalone
hosts select this mode with `--runtime docker --docker-network <network>` and
reachable AMQP/S3 settings. Docker must be running Linux containers. The spawner
uses the Docker Engine API; no Docker CLI is needed inside its image.

Enabling this profile gives the spawner control of the host Docker daemon.
Spawned agents receive no Docker socket, host mounts, published ports or elevated
capabilities. Package files cannot request these privileges.

### During a mission

For runtime spawning, have the controller dispatch `spawn-agent` with:

```json
{"package":"github.com/defos-ai/commander-example-agent@v0.1.0"}
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
packages, moving tags such as `stable`, and Playground/AIO integration are not
implemented. The default runtime is native, including in its Dockerfile;
the optional Compose service explicitly selects Docker mode.

`BUS_URL` and `STORAGE_URL` configure the shared providers. Children receive those
settings and explicit process/S3 settings, without inheriting API, MCP or model
credentials. S3 credentials must be available as environment settings; host profile
files are not shared. Native processes run as the spawner's OS user, not in a sandbox.

Launch records live in `./state/spawner` (`--state-dir` or `SPAWNER_STATE_DIR`).
Native retries reuse the live child in the same host. After a native host restart,
existing requests report uncertainty; inspect old processes before requesting
replacements with a new state directory.

Docker retries also work after a spawner restart: they verify the saved ownership
labels, image, network and bootstrap configuration, then await registration.
Missing or stopped containers are never recreated or restarted by a retry;
request a replacement with a new task or startup slot. Containers use no automatic
restart policy. Remove them explicitly with `docker rm -f <container-id>` before
`docker compose down`; they are separate containers, not Compose services.
Keep `spawner-state` while these containers exist. Only one Docker spawner may
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

Tests use in-memory HTTP fixtures and a locally built child agent. They need no
GitHub credentials, Docker or external services.

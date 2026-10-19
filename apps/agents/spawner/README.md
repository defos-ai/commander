# Spawner

The spawner is a Commander agent that starts published agents as native child
processes. It exposes `spawn-agent` and can also start packages on launch.

## Run

From the repository root, using the same bus and artifact storage as your controller:

```sh
go run ./apps/agents/spawner/cmd --bus-url ./state/bus --storage-url ./state/storage
```

To start a published package immediately, add:

```sh
--spawn-agents github.com/defos-ai/commander-example-agent@v0.1.0
```

Repeat the flag for multiple instances. The package release must contain a valid
`agent.yaml` and a native executable for your OS and architecture.

For runtime spawning, have the controller dispatch `spawn-agent` with:

```json
{"package":"github.com/defos-ai/commander-example-agent@v0.1.0"}
```

It verifies the download, assigns an agent ID, starts the process and waits for
that exact ID and type to announce themselves. Only the eight-character hex suffix
is passed as `COMMANDER_AGENT_ID_SUFFIX`; each agent adds its own fixed type.
The result includes the PID and action readiness. Missing requirements do not
prevent registration; the controller still determines when an agent is eligible
for work.

Children stay running after the action finishes and stop during spawner shutdown.
Linux also stops their process group; other platforms stop the direct child only.
Use a separate provisioning operation before a mission that needs the new actions.

Use packages from authors you trust. A checksum verifies downloaded bytes against
the manifest; it does not establish whether the program is safe to run.

## Current scope

The host supports public GitHub releases and native execution. Docker spawning
and Playground/AIO/Compose integration are planned next. Private repositories
and moving tags such as `stable` are unsupported. Its Dockerfile runs native
children inside the container.

`BUS_URL` and `STORAGE_URL` configure the shared providers. Children receive those
settings and explicit process/S3 settings, without inheriting API, MCP or model
credentials. S3 credentials must be available as environment settings; host profile
files are not shared. Native processes run as the spawner's OS user, not in a sandbox.

Launch records live in `./state/spawner` (`--state-dir` or `SPAWNER_STATE_DIR`).
Retries in the same host reuse the live child. After any restart, including a
clean shutdown, existing requests fail closed because ownership can no longer be
proven; automatic adoption or replacement is not implemented. Inspect old
processes before starting replacements with a new state directory. Registration
waits default to 30 seconds
(`--registration-timeout`); a timed-out or canceled wait leaves the child owned
by the spawner for a subsequent retry or host shutdown.

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

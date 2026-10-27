# Spawner

Install published Commander agents as native processes or Docker containers.
In Playground, ask:

> Install the web agent.

You can also supply any compatible GitHub package URL. No catalog entry is required.

## Run

Compose and all-in-one include the spawner. To run it separately from source:

```sh
go run ./apps/agents/spawner/cmd --bus-url ./state/bus --storage-url ./state/storage
```

Use the controller's bus and artifact storage. Native mode is the default;
installed agents need any host dependencies listed in their README.
Run with `--help` for flags, or see [release binaries](../../../docs/releases.md#native-binaries).

### Docker

Start the default Compose stack:

```sh
docker compose up --build --wait
```

Requested agents run as sibling Linux containers on Commander's network.
The spawner requires access to the host Docker daemon; give that access only
to a deployment you trust.

For a separate Docker-mode spawner, set `SPAWNER_RUNTIME=docker` and
`SPAWNER_DOCKER_NETWORK` to an existing network. Use AMQP and S3 addresses
reachable from child containers. See [deployment](../../../docs/deployment.md).

### During a mission

Through MCP, read the `install-agent` mission and `spawn-agent` action schemas.
Start an `install-agent` operation and assign `spawn-agent` with the same input:

```json
{"package":"github.com/defos-ai/commander-example-agent@v0.2.0","visibility":"public"}
```

A GitHub URL or `@stable` selects the repository's promoted **Latest** release.
Use `@vMAJOR.MINOR.PATCH` to choose an exact version. Retrying the same execution
keeps its selected version.

Review the result and the new agent's action readiness, then complete installation.
Install agents in a separate operation before starting missions that need them.
Only run packages from authors you trust.

### Private GitHub packages

Pass `"visibility":"private"`. The spawner uses its `GITHUB_TOKEN` environment
variable, or opens the regular Playground/Admin secret form if none is set.
Never paste a token into chat. Public installations use anonymous downloads.

For private GHCR images, the token also needs package access; a classic personal
access token requires `read:packages`. Other registries use anonymous pulls.
See [GitHub's registry authentication guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry#authenticating-to-the-container-registry).

## Configuration

Compose and all-in-one set up the runtime and secret exchange automatically.
For a separate host:

| Setting | When to use it |
| --- | --- |
| `BUS_URL`, `STORAGE_URL` | Connect to the controller's providers |
| `SPAWNER_RUNTIME=docker` | Launch containers instead of native processes |
| `SPAWNER_DOCKER_NETWORK` | Select the Docker network; required in Docker mode |
| `SPAWNER_STATE_DIR` | Change native downloads and working directories from `state/spawner` |
| `SECRETS_ENCRYPTION_KEY` | Use the API's key for managed secret forms |
| `GITHUB_TOKEN` | Preconfigure access to private GitHub packages |

Children receive provider settings, not your API, MCP or model tokens. The
official builder package also receives the secrets encryption key; Docker
administrators can inspect that key in its container environment.

Spawn results put runtime-specific identity in `runtime_details`: `pid` for
native processes and `container_id` for Docker containers. Kubernetes spawning
is not implemented. Its reserved payload contains `namespace`, `pod_name` and
`pod_uid`, but `SPAWNER_RUNTIME=kubernetes` remains unsupported. See the
[planned runtime contract](AGENTS.md#planned-kubernetes-runtime) before preparing
a deployment; its Kubernetes settings are not read by the current CLI.

## Stop or replace agents

- Native children stop with the spawner. In all-in-one, temporary state is
  removed on exit.
- Docker children keep running when the spawner stops. Follow
  [scoped teardown](../../../docs/deployment.md#stopping-and-resetting-agents)
  before `docker compose down`.
- Keep controller execution records while children exist. If launch status is
  uncertain, inspect the process or container before requesting a replacement.
  A new task creates a new instance; retrying does not restart a stopped one.

## Development

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/spawner/...
go vet ./apps/agents/spawner/...
```

Focused tests use local fixtures. See [API/MCP tests](../../../framework/tests/README.md#mcp-api-tests)
for service-backed scenarios and [AGENTS.md](AGENTS.md) for implementation contracts.

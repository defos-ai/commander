# Spawner

Add capabilities to operational workflows by installing published Commander agents
as native processes, Docker containers or Kubernetes Pods. Review the launch result
and registered action readiness before assigning work.

In Playground, ask:

> Install the web agent.

You can also supply a GitHub package URL without a source-list entry.
Spawner currently permits repositories owned by `github.com/defos-ai` only.
Other owners are blocked until operator-only installation approval is available.

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

### Kubernetes

Run inside the cluster with `SPAWNER_RUNTIME=kubernetes` and configure:

| Setting | Value |
| --- | --- |
| `SPAWNER_KUBERNETES_NAMESPACE` | Required child namespace |
| `SPAWNER_KUBERNETES_SERVICE_ACCOUNT` | Required child ServiceAccount |
| `SPAWNER_KUBERNETES_CONFIG_MAP` | Required child ConfigMap reference |
| `SPAWNER_KUBERNETES_SECRET` | Required child Secret reference |
| `SPAWNER_KUBERNETES_POD_PREFIX` | Optional literal prefix; empty by default |
| `SPAWNER_KUBERNETES_IMAGE_PULL_SECRETS` | Optional comma-separated pull Secret references |

The spawner uses its mounted ServiceAccount credentials automatically. Its role
needs Pod `get`, `create` and `patch` permissions in the child namespace. Use an
AMQP/AMQPS `BUS_URL` and S3 `STORAGE_URL` for the spawner; the child Secret must
contain `BUS_URL` and `STORAGE_URL` for the same providers. The ConfigMap may supply
`AWS_REGION`, `AWS_ENDPOINT_URL_S3` and `S3_USE_PATH_STYLE`.

Children use UID/GID 1000, requests `100m/128Mi`, and limits `500m/512Mi`; choose
compatible images. Include any ServiceAccount pull Secret references in the
configured list so admission matches the launch policy. Child API-token mounting
is disabled. The official Builder package is not supported in Kubernetes yet.

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

Use MCP `operation_logs`, optionally filtered by `task_id`, to follow package
selection, preparation, launch reconciliation and registration waits. Long stages
emit periodic liveness updates. Use the saved task result and agent readiness to
assess the installation.

### Private GitHub packages

Pass `"visibility":"private"`. The spawner uses its `GITHUB_TOKEN` environment
variable, or opens the regular Playground/Admin secret form if none is set.
Never paste a token into chat. Public installations use anonymous downloads.

For Docker GHCR pulls, the token also needs package access; a classic personal
access token requires `read:packages`. Other registries use anonymous pulls.
Kubernetes uses image-pull Secrets independently of the package-download token.
See [GitHub's registry authentication guide](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry#authenticating-to-the-container-registry).

## Configuration

Compose and all-in-one set up the runtime and secret exchange automatically.
For a separate host:

| Setting | When to use it |
| --- | --- |
| `BUS_URL`, `STORAGE_URL` | Connect to the controller's providers |
| `SPAWNER_RUNTIME` | Select `native` (default), `docker` or `kubernetes` |
| `SPAWNER_DOCKER_NETWORK` | Select the Docker network; required in Docker mode |
| `SPAWNER_STATE_DIR` | Change native downloads and working directories from `state/spawner` |
| `SECRETS_ENCRYPTION_KEY` | Use the API's key for managed secret forms |
| `GITHUB_TOKEN` | Preconfigure access to private GitHub packages |

Children receive provider settings, not your API, MCP or model tokens. The
official builder package in native/Docker mode also receives the secrets encryption key; Docker
administrators can inspect that key in its container environment.

Spawn results put runtime-specific identity in `runtime_details`: `pid` for
native processes, `container_id` for Docker containers, and `namespace`, `pod_name`
and `pod_uid` for Kubernetes. See the [runtime contract](AGENTS.md#kubernetes-runtime)
for ownership and recovery behavior.

## Stop or replace agents

- Native children stop with the spawner. In all-in-one, temporary state is
  removed on exit.
- Docker children keep running when the spawner stops. Follow
  [scoped teardown](../../../docs/deployment.md#stopping-and-resetting-agents)
  before `docker compose down`.
- Kubernetes Pods also survive spawner shutdown. The existing controller
  `StopAgent` protocol requests graceful shutdown; operators remove stopped Pods
  after verifying the saved namespace/name/UID and spawn ownership labels.
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

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.

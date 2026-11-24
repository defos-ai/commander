# Install and publish agent packages

An agent package is a GitHub release with an `agent.yaml` manifest and a native
executable, a Docker image, or both. Start from
[commander-example-agent](https://github.com/defos-ai/commander-example-agent)
to build one.

## Sources and stable references

In Playground, ask:

> Install the builder agent.

Commander can look it up with MCP's `get_agent_sources`. You can also provide a
GitHub package directly:

> Install github.com/defos-ai/commander/apps/agents/builder@stable

Packages owned by `github.com/defos-ai` proceed automatically. For other owners,
`spawn-agent` asks an installation question: the operator answers first,
then the model may answer after `fallback_at`. Installation waits for a saved
Yes; approval applies only to that execution.

| Reference | Version |
| --- | --- |
| `https://github.com/owner/repository` | Promoted GitHub **Latest** release |
| `github.com/owner/repository@stable` | Same as an unversioned URL |
| `github.com/owner/repository@v0.4.0` | Exact version |

Use explicit versions for reproducible installations. `stable` needs a published
stable version marked Latest; it does not fall back to other tags. Each new
installation creates an instance. Check the result and action readiness before
starting dependent work.

To upgrade a running instance, follow [stop and replace](features/agents.md#replace-an-agent),
then install the desired pinned release in a new operation.

The official [agent source list](../sources/README.md) helps discovery through
MCP. It contains metadata, without saved trust approvals or an installation allowlist.

Before launching, assign Spawner's `inspect-agent` action through the controller
to read the selected release's dependencies and declared delivery platforms. Use
its exact `package` reference for the later spawn. Inspection starts no agent and
does not grant installation approval or establish readiness. Prepare native software
in the spawner's execution environment; Docker all-in-one children run inside its
container. Docker/Kubernetes software belongs in the published image and deployment.

### Private repositories

Pass `visibility: "private"` with the package. The spawner uses its `GITHUB_TOKEN`,
or asks for it through the Playground/Admin secret form. Never put a token in
chat or an action input.

Private GHCR images also need package access. For personal access tokens, use a
classic token with `read:packages`; grant repository access as needed. Other
registries are pulled anonymously. See [GitHub registry authentication](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry#authenticating-to-the-container-registry).

## Release manifest

Publish `agent.yaml` with each release. Include only the platforms you build.
The example below uses placeholder hashes; replace them with the actual binary
checksum and pushed image digest:

```yaml
schema_version: 1
type: example-agent
version: v0.4.0
responsibilities:
  - Analyze text and produce a Markdown summary.
actions:
  - name: analyze-text
    description: Analyze text and propose a summary.
native:
  linux/amd64:
    asset: commander-example-agent_linux_amd64
    sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
docker:
  image: ghcr.io/defos-ai/commander-example-agent@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
```

- Match `type` to the executable's registered agent type and `version` to the release.
- List responsibilities and action summaries so people and models can assess the package.
- For native delivery, attach the named executable to the same release; do not use an archive.
- For Docker, publish a digest-pinned image with its entrypoint and dependencies.
- Provide at least one delivery mode. Declare external software in optional
  `dependencies`, with platform-specific setup instructions in the agent's README.

For example, an agent that uses an external browser can declare:

```yaml
dependencies:
  - name: lightpanda
    description: Browser runtime; install a compatible build for the execution host.
    version: "<supported version or range>"
```

Use the same declarations in `AgentConfig.Dependencies` and the release manifest.
Names and descriptions are required; version guidance is optional. Omit the list
or use `dependencies: []` when there are no declarations. Match executable names
reported by action discovery. The agent checks actual readiness; installation
steps depend on the execution host's platform.

### Source template

Reuse the example agent's release workflow, or call
[`manifest.Render`](../framework/agent/manifest/manifest.go) with your build's
checksums and image digest. It validates the manifest and renders `agent.yaml`.
Export pure `Responsibilities()` and `ActionDescriptions()` helpers so release
tooling and live registration share their descriptions.

Use the same exact release version for `agent.yaml`, image tags and the running
agent's `AgentConfig.Version`. The built-in release workflows stamp this value
into native and Docker builds; embedded agents retain their own package version.
Existing unstamped binaries remain unreported. See the
[agent build contract](../apps/agents/AGENTS.md#package-contract) when adopting it.

Build, test and generate metadata from the same revision. Keep the release as a
draft until all assets are uploaded. For Commander's built-in packages, follow
[the release checklist](releases/README.md#agent-packages).

## Resolving a package

Standalone repositories use a root release tag; modules in a shared repository
use a module-prefixed tag. Both publish an asset named `agent.yaml`:

| Package reference | GitHub release tag |
| --- | --- |
| `github.com/defos-ai/commander-web-agent@v0.2.0` | `v0.2.0` |
| `github.com/defos-ai/commander/apps/agents/builder@v0.8.0` | `apps/agents/builder/v0.8.0` |

Module `@stable` references use the repository's root Latest version to choose the
corresponding module release. Publication is required; a version in `go.mod` or
a source entry does not make a package available.

The spawner verifies the native checksum or pulls the pinned image. It does not
build source, run repository Compose files or install host dependencies. Install
only packages whose authors you trust.

## Bootstrap and registration

For an agent to support spawning, its host must accept:

| Setting | Use |
| --- | --- |
| `BUS_URL` | Connect to Commander's message bus |
| `STORAGE_URL` and applicable S3 settings | Upload artifact files |
| `COMMANDER_AGENT_ID_SUFFIX` | Build the instance ID from the fixed agent type plus eight lowercase hex characters |

Use `id.AgentIDFromSuffix` to validate a supplied suffix before opening providers.
Generate a fresh ID when none is supplied. Use the [example agent](https://github.com/defos-ai/commander-example-agent)
for the complete CLI and factory pattern.

Pass only the child's required environment. Never inherit the parent's entire
credential set. Managed-secret agents receive requested values from the controller
without an encryption key. Spawner does not mount or forward that key.
See [builder secrets](../apps/agents/builder/README.md#secrets).

Registration means the process connected. Check action readiness too: the agent
may still need a secret, executable or configuration.

## Startup, retries and lifetime

| Deployment | Where additional agents run |
| --- | --- |
| Native all-in-one | Child processes on your host |
| Docker AIO | Child processes inside the AIO container |
| Compose | Sibling containers on the deployment network |
| Kubernetes spawner runtime | Pods in its configured namespace |

Only spawner starts by default. Additional agents are installed through
chat/MCP in a separate `install-agent` operation, before starting work that needs
their actions. `spawn-agent` accepts `count` from 1 to 100, defaulting to one.
Larger requests propose individual child launch tasks; review their results and
readiness before starting dependent work. Pin a version when every instance must
use the same release.

If installation fails with an uncertain outcome, inspect the process or container
before requesting a new instance. A spawner restart leaves Docker children running;
native restart recovery is not supported. Stop owned containers before removing
their deployment network or data using the [teardown commands](deployment.md#stopping-and-resetting-agents).

## Verify your package

1. Publish the native binaries, Docker image and generated manifest.
2. Install the pinned release in native and Docker modes you support.
3. Check that the expected agent ID registers and its actions become ready.
4. Add a discoverable [agent source](../sources/README.md) when ready to share it.

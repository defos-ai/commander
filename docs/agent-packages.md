# Install and publish agent packages

An agent package is a GitHub release with an `agent.yaml` manifest and a native
executable, a Docker image, or both. Start from
[commander-example-agent](https://github.com/defos-ai/commander-example-agent)
to build one.

## Sources and stable references

In Playground, ask:

> Install the web agent.

Commander can look it up with MCP's `get_agent_sources`. You can also provide any
trusted GitHub repository directly:

> Install https://github.com/defos-ai/commander-web-agent

| Reference | Version |
| --- | --- |
| `https://github.com/owner/repository` | Promoted GitHub **Latest** release |
| `github.com/owner/repository@stable` | Same as an unversioned URL |
| `github.com/owner/repository@v0.2.0` | Exact version |

Use explicit versions for reproducible installations. `stable` needs a published
stable version marked Latest; it does not fall back to other tags. Each new
installation creates an instance. Check the result and action readiness before
starting dependent work.

The official [agent source list](../sources/README.md) helps discovery through
MCP. It contains metadata, without saved trust approvals or an installation allowlist.

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
version: v0.2.0
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
- Provide at least one delivery mode. Native prerequisites belong in the agent's README.

### Source template

Reuse the example agent's release workflow, or call
[`manifest.Render`](../framework/agent/manifest/manifest.go) with your build's
checksums and image digest. It validates the manifest and renders `agent.yaml`.
Export pure `Responsibilities()` and `ActionDescriptions()` helpers so release
tooling and live registration share their descriptions.

Build, test and generate metadata from the same revision. Keep the release as a
draft until all assets are uploaded. For Commander's built-in packages, follow
[the release checklist](releases.md#agent-packages).

## Resolving a package

Standalone repositories use a root release tag; modules in a shared repository
use a module-prefixed tag. Both publish an asset named `agent.yaml`:

| Package reference | GitHub release tag |
| --- | --- |
| `github.com/defos-ai/commander-web-agent@v0.2.0` | `v0.2.0` |
| `github.com/defos-ai/commander/apps/agents/builder@v0.2.0` | `apps/agents/builder/v0.2.0` |

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
credential set. Managed-secret agents need a separately configured encryption key.
Compose's optional builder shares the API's key; the spawner retains its narrow
trusted bootstrap for the official builder package.
See [builder secrets](../apps/agents/builder/README.md#secrets).

Registration means the process connected. Check action readiness too: the agent
may still need a secret, executable or configuration.

## Startup, retries and lifetime

| Deployment | Where additional agents run |
| --- | --- |
| Native all-in-one | Child processes on your host |
| Docker AIO | Child processes inside the AIO container |
| Compose | Sibling containers on the deployment network |

Only spawner starts by default. Additional agents are installed through
chat/MCP in a separate `install-agent` operation, before starting work that needs
their actions. One spawn task creates one instance; use separate tasks for more.

If installation fails with an uncertain outcome, inspect the process or container
before requesting a new instance. A spawner restart leaves Docker children running;
native restart recovery is not supported. Stop owned containers before removing
their deployment network or data using the [teardown commands](deployment.md#stopping-and-resetting-agents).

## Verify your package

1. Publish the native binaries, Docker image and generated manifest.
2. Install the pinned release in native and Docker modes you support.
3. Check that the expected agent ID registers and its actions become ready.
4. Add a discoverable [agent source](../sources/README.md) when ready to share it.

# Builder Agent

The `builder-agent` creates Commander agents with Codex, checks them with Go,
and exports their source as an artifact. `checkout-template` clones the
[example agent](https://github.com/defos-ai/commander-example-agent);
`build-agent` implements the requested behavior. `publish-agent` publishes
a reviewed source artifact to GitHub. `release-agent` optionally runs and verifies
that repository's release workflow.

## Run

Install Git, Go 1.26.7+ and [Codex CLI](https://learn.chatgpt.com/docs/non-interactive-mode), then run
from the repository root:

```sh
go run ./apps/agents/builder/cmd
```

Connect the [controller](../../controllers/README.md#quick-start) to the same
`BUS_URL` and `STORAGE_URL`. Both default to local directories under `state/`.
The builder registers as `builder-agent-{id}`. Each action reports its own missing
executables or managed secrets without preventing the host from starting.

To assign its instance suffix, use `--agent-id-suffix=a1b2c3d4` or
`COMMANDER_AGENT_ID_SUFFIX`. The flag takes precedence; an explicit empty flag
generates a fresh ID. A nonempty suffix must be eight lowercase hex characters;
each running instance needs a unique ID.

In an operation, request `checkout-template` with `{}` as input. The action uses
the fixed public template; it requires no GitHub token. Each call creates a
separate checkout under `state/builder-workspaces/<workspace_id>/source`.
Change the root with `--workspace-dir` or `BUILDER_WORKSPACE_DIR`.

Then request `build-agent` on the same builder with:

```json
{
  "workspace_id": "<checkout result>",
  "instructions": "Create a word-counting agent. Count words separated by whitespace.",
  "actions": [
    {
      "name": "count-words",
      "description": "Count the words in supplied text.",
      "input_schema": {
        "type": "object",
        "properties": {"text": {"type": "string"}},
        "required": ["text"],
        "additionalProperties": false
      },
      "output_schema": {
        "type": "object",
        "properties": {"count": {"type": "integer", "minimum": 0}},
        "required": ["count"],
        "additionalProperties": false
      }
    }
  ]
}
```

Provide 1–100 action contracts with unique names, descriptions, object input/output
schemas and optional `required_secrets` names. The builder validates these contracts
before running Codex and includes them in its instructions. Secret names describe
what the generated agent needs; secret values use the managed-secret form.

It uses the managed `CODEX_API_KEY`, runs Codex and Go tests/build, then checks
the release structure and the actions registered by the generated agent. Registered
names, descriptions, input/output schemas and required secret names must match
the requested contracts. Inspection calls the constructor without discovery or
task execution; the generated agent's secret values are not needed.
It returns the workspace ID, source file count, agent type, module path,
binary name and a `source.tar.gz`
artifact. Supply the key through Playground's secret form or Admin's agent
secrets page; never put it in the instructions. The bundled `build-agent` mission
connects checkout and build with a `same-agent` rule and model review after each
stage. Ask Playground to build an agent and describe its behavior; it defines
the action contracts before checkout and passes them unchanged to `build-agent`.
Publication and release are optional actions in that mission.

The structure check validates the agent identity, module and manifest against
the template's release entry points, including the requested action summaries.
A failed check preserves the workspace for revision and returns no source artifact.
Contract matching checks declarations; the generated agent's tests still need to
cover its behavior. Verification needs no GitHub token and does not publish anything.
It does not prove that all six native platforms, Docker builds or GitHub release jobs will
succeed; those checks run when the agent is released.

## Publish source

After reviewing a successful build, choose whether to publish its source to
`owner/repository` before completing the build mission. If you agree, its optional
`publish-agent` action uses the saved artifact ID and full file metadata, so it
can run on any builder connected to the same storage. Publication and release use the
managed `GITHUB_TOKEN`, supplied through the secret form. Skipping it still
completes the build with its source artifact.

Choose **Public** (recommended) or **Private**. To set a deployment default,
configure `BUILDER_DEFAULT_REPO_VISIBILITY=public` or `private` on the builder.
The action advertises that default; without one, Playground asks and waits for
your choice before publishing. An explicit choice overrides the default.

Publication creates a repository or uses an empty one, preserves Commander
attribution in its README, and returns the repository and commit links. It never
changes an existing repository's visibility or overwrites conflicting history.
Source publication does not produce a release package or install the agent.

## Release the generated agent

After publication, choose whether to release it and provide a version such as
`v0.1.0`. This stays in the same build mission; finishing with source only is
also supported. `release-agent` takes the published `repository` and `commit_sha`,
the build's `agent_type`, and your chosen `version`. The repository's default
branch must still point to the reviewed commit.

The managed `GITHUB_TOKEN` needs repository **Actions: write** and **Contents: read**
for dispatch and verification. Publication additionally needs Contents write and,
for repository creation, the appropriate account permissions. The generated
workflow uses its own GitHub Actions token to publish binaries and the Docker image;
allow its repository access to any existing GHCR package.

While CI runs, the action proposes bounded status checks using the same
`workflow_run_id`. A successful result verifies the version tag, published
`agent.yaml`, six native asset checksums and digest-pinned image metadata, then
returns `github.com/owner/repository@v0.1.0`. It does not install the agent or
pull the image. Repository visibility is preserved; GHCR access is configured
separately.

For a failed workflow, inspect its link and retry the failed jobs in that same
GitHub run. Resume observation with its `workflow_run_id`. If dispatch is
uncertain, inspect GitHub before creating another release task; an execution
checkpoint prevents repeating that execution's dispatch, without guaranteeing
exactly-once publication.

After a verified release, Commander offers **Install** or **Finish**, unless you
already specified your choice. Installation uses the exact released version and
visibility in a separate `install-agent` operation, then checks the agent's action
readiness. Choosing Finish keeps the source artifact and release links. An
installation failure leaves the successful build and release available.

## Bundled deployment

The repository [Compose stack](../../../compose.yaml) and
[Playground all-in-one](../../playground/README.md#native-one-executable) start
builder and spawner agents from the current build, without downloading packages.

Compose runs the builder with Git, Go and Codex installed and a `builder-workspaces` volume
at `/home/commander/workspaces`; checkouts survive container recreation. Playground
all-in-one embeds the builder host and keeps its workspaces in temporary session
state, deleted on exit. The all-in-one Docker image includes these tools; native
installations use tools from the host. Missing tools make only the affected
actions unavailable without preventing startup.

## Release package

The next Commander release will publish `agent.yaml` on the builder's module-tag
release, such as `apps/agents/builder/v0.2.0`. Once published, ask chat or MCP to
run `spawn-agent` with:

```json
{"package":"github.com/defos-ai/commander/apps/agents/builder@v0.2.0"}
```

This package provides native executables for Linux, macOS and Windows on
amd64/arm64, plus the `linux/amd64` image
`ghcr.io/defos-ai/commander-builder-agent`, pinned by digest in the manifest.
The builder image includes Git, Go and Codex CLI 0.114.0, pinned for compatibility
with its unprivileged container sandbox. Native executables require these tools
on the host.
Existing releases are unchanged; use the source command until publication.
See [agent packages](../../../docs/agent-packages.md).

The spawner supplies its managed-secrets encryption key to this official builder
package automatically. It checks both the exact repository/module and the
`builder-agent` manifest type; a fork or another package claiming that type
receives no key. Package spawning does not add a persistent workspace volume.

## Secrets

The host accepts `SECRETS_ENCRYPTION_KEY` for actions that require managed secrets.
Compose shares its persisted key with the default builder; Playground all-in-one
passes its session cipher directly to the embedded builder. Additional official
builder packages receive the key through the spawner's trusted bootstrap.
For a separately started builder, use the same key as the API;
`checkout-template` does not need it. Docker administrators can read the key from
the builder container's environment. Action secrets still arrive through the
managed-secret flow; the spawner does not copy GitHub or model tokens into the
builder.

When embedding the builder, pass an initialized
[`crypto.SecretCipher`](../../../framework/crypto/README.md) through
`cli.Options.SecretCipher` to `cli.RunWithOptions`. It takes precedence over the
environment key and is validated before providers start.

## Current scope

Successful workspaces remain on disk for later work; all-in-one removes them
on exit. Failed or cancelled checkouts are removed. Workspace IDs refer to this
builder's local storage, so use the mission's `same-agent` rule for checkout and
build instead of handing them to another builder instance.

Checkout does not execute template code. Git uses isolated configuration without
host credentials, hooks, proxies or submodule checkout. It needs direct HTTPS
access to GitHub.

Codex runs with its workspace-write sandbox and temporary configuration. Go and
release-structure checks use `GOWORK=off` and a clean environment without the
Codex key, but execute generated code as the builder's operating-system user.
Use a builder deployment you trust for code execution. Source publication is
optional within the build mission, followed by an optional verified release.
Starting the generated agent remains a separate installation step.

## Development

See [AGENTS.md](AGENTS.md) for implementation guidance and the
[agent guide](../README.md) for action contracts.

```sh
go test -race -count=1 -timeout=3m ./apps/agents/builder/...
go vet ./apps/agents/builder/...
```

Tests use local fixtures and need no network or paid model requests.

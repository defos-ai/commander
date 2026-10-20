# Builder Agent

The `builder-agent` prepares source workspaces for creating Commander agents.
Its first action, `checkout-template`, clones the
[example agent](https://github.com/defos-ai/commander-example-agent) and returns
a workspace ID and the exact template revision.

## Run

Install Git, then run from the repository root with Go 1.26.7+:

```sh
go run ./apps/agents/builder/cmd
```

Connect the [controller](../../controllers/README.md#quick-start) to the same
`BUS_URL` and `STORAGE_URL`. Both default to local directories under `state/`.
The builder registers as `builder-agent-{id}`. If Git is missing,
`checkout-template` reports it as a missing requirement.

To assign its instance suffix, use `--agent-id-suffix=a1b2c3d4` or
`COMMANDER_AGENT_ID_SUFFIX`. The flag takes precedence; an explicit empty flag
generates a fresh ID. A nonempty suffix must be eight lowercase hex characters;
each running instance needs a unique ID.

In an operation, request `checkout-template` with `{}` as input. The action uses
the fixed public template; it requires no GitHub token. Each call creates a
separate checkout under `state/builder-workspaces/<workspace_id>/source`.
Change the root with `--workspace-dir` or `BUILDER_WORKSPACE_DIR`.

## Bundled deployment

The repository [Compose stack](../../../compose.yaml) and
[Playground all-in-one](../../playground/README.md#native-one-executable) start
the spawner agent. Request the builder package through chat or MCP when needed,
or run the source command above against the same bus and artifact storage.

Compose spawns the builder in Docker, with Git installed and workspaces inside
its container at `/home/commander/workspaces`. Playground all-in-one spawns its
native executable; the all-in-one Docker image includes Git, while native
installations use Git from the host. Missing Git makes `checkout-template`
unavailable until it is installed.

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
The builder image includes Git; native executables require Git on the host.
Existing releases are unchanged; use the source command until publication.
See [agent packages](../../../docs/agent-packages.md).

The spawner supplies its managed-secrets encryption key to this official builder
package automatically. It checks both the exact repository/module and the
`builder-agent` manifest type; a fork or another package claiming that type
receives no key. Package spawning does not add a persistent workspace volume.

## Secrets

The host accepts `SECRETS_ENCRYPTION_KEY` for actions that require managed secrets.
Compose and Playground all-in-one configure it automatically when spawning the
official builder. For a separately started builder, use the same key as the API;
`checkout-template` does not need it. Docker administrators can read the key from
the builder container's environment. Action secrets still arrive through the
managed-secret flow; the spawner does not copy GitHub or model tokens into the
builder.

When embedding the builder, pass an initialized
[`crypto.SecretCipher`](../../../framework/crypto/README.md) through
`cli.Options.SecretCipher` to `cli.RunWithOptions`. It takes precedence over the
environment key and is validated before providers start.

## Current scope

Successful workspaces remain on disk for later work; failed or cancelled checkouts
are removed. Workspace IDs refer to this builder's local storage, so they cannot
be handed to another builder instance.

Checkout does not execute template code. Git uses isolated configuration without
host credentials, hooks, proxies or submodule checkout. It needs direct HTTPS
access to GitHub.

Codex builds, source artifacts, GitHub publication and generated-agent startup
are the next stages.

## Development

See [AGENTS.md](AGENTS.md) for implementation guidance and the
[agent guide](../README.md) for action contracts.

```sh
go test -race -count=1 -timeout=3m ./apps/agents/builder/...
go vet ./apps/agents/builder/...
```

Tests use local Git repositories and need no network or model key.

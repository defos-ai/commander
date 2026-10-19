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

The repository [Compose stack](../../../compose.yaml) starts one builder with Git
installed. It uses the shared bus and artifact storage, and saves checkouts in
the `builder-workspaces` volume at `/home/commander/workspaces`.

The builder and API read the same persisted encryption key from the `secrets-key`
volume. Only the initializer writes that volume; the builder mounts it read-only
and loads the key before starting. Keep this key with any backups of encrypted
secrets. See [deployment](../../../docs/deployment.md#secret-encryption).

[Playground all-in-one](../../playground/README.md#native-one-executable) also
includes the builder, with workspaces under the session's local state directory.
Its Docker image includes Git; native installations use Git from the host and
report it as a missing requirement if unavailable.

## Secrets

The host accepts `SECRETS_ENCRYPTION_KEY` for actions that require managed secrets.
Use the same key as the API; `checkout-template` does not need it.

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

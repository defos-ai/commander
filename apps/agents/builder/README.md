# Builder Agent

Build custom agents for internal workflows from explicit action contracts using
Codex. Review the generated source and validation results, then choose whether to
publish and release the agent on GitHub.

After starting a builder, ask Playground:

> Build an agent that counts words in supplied text.

## Run

Enable the optional Compose builder from the repository root:

```sh
docker compose --profile builder up --build --wait
```

Its image includes Git, Go and Codex CLI. All-in-one starts only spawner.

For a separate native builder, install those tools and use Go 1.26.7+:

```sh
go run ./apps/agents/builder/cmd
```

Use the [controller's](../../controllers/README.md#quick-start) `BUS_URL` and
`STORAGE_URL`. For other flags, run the command with `--help`.

## Build an agent

1. Describe the behavior and expected inputs/outputs in Playground.
2. Supply `OPENAI_API_KEY` through the secret form when asked.
3. Review the generated agent and download its `source.tar.gz` artifact in Admin.

The `build-agent` mission defines action contracts, checks out the
[example template](https://github.com/defos-ai/commander-example-agent), generates
code, and runs Go and package checks.
Failed checks preserve the workspace for revision. Review the generated tests
and behavior before deploying it.

Use MCP `operation_logs`, optionally filtered by `task_id`, to follow builder
stages and periodic “still running” updates. Set `order: "desc"` to read recent
entries. Progress logging is best effort; use the saved task result to determine
the outcome.

For direct MCP use, read `describe_mission` for `build-agent` and
`describe_action` for each action's input schema. Checkout and build must run on
the same builder; the bundled mission handles this routing.

## Publish source

After a successful build, choose whether to publish to `owner/repository`.
You can finish with the source artifact alone.

- Choose **Public** to share the agent, or **Private**.
- Supply `GITHUB_TOKEN` through the secret form. It needs permission to create
  the repository and write its contents.
- Use a new or empty repository; conflicting history is rejected.

To set a default visibility, configure `BUILDER_DEFAULT_REPO_VISIBILITY=public`
or `private` on the builder. An explicit choice overrides it.

## Release the generated agent

Choose a version such as `v0.1.0` after publishing. The release action runs the
repository's GitHub workflow and returns a verified package reference when it
succeeds. Keep the default branch at the reviewed commit until dispatch.

The token also needs **Actions: write** and **Contents: read**. Grant the
repository access to any existing GHCR package it publishes.

For a failed workflow, open its run link and retry failed jobs in that same run.
If dispatch was uncertain, inspect GitHub before requesting another release.

After release, choose **Install** or **Finish**. Installation checks the new
agent's readiness; its failure leaves your build and release available.

## Secrets

The optional Compose builder mounts the API's encryption key automatically.
Enter model and GitHub tokens in Playground's secret form or Admin's
**Agents → Manage secrets**; never paste them into chat or action inputs.

For a separately started builder, set `SECRETS_ENCRYPTION_KEY` to the API's key.
The spawner supplies it automatically only to the official builder package.
See [secret setup](../../../framework/secrets/README.md).

## Workspaces and execution

| Deployment | Workspace storage |
| --- | --- |
| Compose (`builder` profile) | `builder-workspaces` volume; retained across container recreation |
| Separate builder | `state/builder-workspaces`; override with `BUILDER_WORKSPACE_DIR` |

Use a builder deployment you trust to execute generated code. Codex uses its
workspace sandbox, but Go tests and builds run as the builder's operating-system
user. Package checks verify declarations and structure; release CI must still
pass native and Docker builds.

For installing a released builder elsewhere, see [agent packages](../../../docs/agent-packages.md).

## Development

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/builder/...
go vet ./apps/agents/builder/...
```

Tests use local fixtures without paid model requests. See [AGENTS.md](AGENTS.md)
for implementation contracts and [agent development](../README.md#development-and-testing).

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.

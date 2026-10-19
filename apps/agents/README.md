# Agents

## Overview

Agents advertise actions, execute controller-selected tasks and return results
and artifacts. They can propose follow-up actions; the controller applies mission
rules and Commander handles open decisions.

The bundled [web agent](web/README.md) discovers pages and checks links, SEO and
technology signals over HTTP. The [browser agent](browser/README.md) adds rendered
accessibility checks and screenshots. See the [architecture](../../docs/overview.md) for component
boundaries and [action contracts](../../framework/README.md) for shared interfaces.

The [builder agent](builder/README.md) starts agent creation by checking out the
example template into an isolated workspace. Build and deployment actions are
still to come.

The planned [agent package contract](../../docs/agent-packages.md) describes
versioned binaries/images and bootstrap for agents created from
`commander-example-agent`. Spawner support is not implemented yet.

## Quick start

With Go 1.26.7+, run from the repository root:

```sh
go run ./apps/agents/web/cmd
```

Start the [controller](../controllers/README.md#quick-start) with matching bus and
artifact storage settings. See [native requirements](browser/README.md#native-agent)
and [deployment](../../docs/deployment.md) for configuration.

## Development and testing

### Add an agent

Create `apps/agents/<agent-type>` with the module path
`github.com/defos-ai/commander/apps/agents/<agent-type>`, using the web agent's
[go.mod](web/go.mod), [constructor](web/web_agent.go) and
[CLI](web/cli/cli.go) as a starting point. Register it in the workspace:

```sh
go work use ./apps/agents/<agent-type>
```

Supply your agent type, action catalog and artifact storage to
`agent.NewAgent`. The [provider factory](web/cli/factory.go) shows how to
connect it to the bus and storage. For repository coding conventions, see
[AGENTS.md](AGENTS.md).

### Add an action to an existing agent

Use [`action.NewFunc`](../../framework/agent/action/func.go) for typed JSON
input/output; it derives schemas from your Go types. For file attachments,
implement [`action.Executor`](../../framework/agent/action/action.go) directly.
Declare the action's name, implement its behavior, then
add its constructor to the [action catalog](web/actions/actions.go).
[Check-SEO](web/actions/check_seo.go) is a small JSON action example.

### Action readiness

Install required programs and supply settings at deployment. Missing dependencies
make an action unavailable; other actions can still serve. See
[browser discovery](browser/actions/browser.go) for an implementation and
[mission preflight](../../docs/abstractions.md#discovery-and-preflight) for how
readiness affects scheduling. Each host's `--actions` flag selects its exposed
catalog.

Actions can declare persistent secret names in `Action.RequiredSecrets`.
Secrets belong to the registered agent type, so its instances share the same
named secrets. Declarations remain visible when an action is ready.
For an agent that needs managed secrets, configure `AgentConfig.Secrets` with a
decryptor. The [builder host](builder/README.md#secrets) supports this configuration;
the bundled web and browser hosts do not need it.

Use `action.Secret(ctx, name)` in discovery or execution
to obtain a declared value. Missing or expired values block only the affected
actions. See [secret delivery and refresh](../../framework/secrets/README.md#agent-requirements).

### Return files

Set `Result.Artifacts` to grouped files supplied as bytes or local paths. The
runtime uploads them and records their metadata. See
[capture-page](browser/actions/capture_page.go), which returns desktop/mobile
PNGs under one artifact.

### Propose follow-up work

Use `FuncConfig.NextActions` or `Result.NextActions` to propose an action name,
JSON input and optional reason. [Discover-pages](web/actions/discover_pages.go)
proposes five checks per page. The controller queues valid proposals by default;
mission rules can require model review or collect child results. See
[continuations and scheduling](../../docs/abstractions.md#next-actions-and-continuations).

### Develop an agent in a private repository

Imports use repository paths such as `github.com/defos-ai/commander/framework`.
No releases are published yet, so keep your agent in its own module and pin a
Commander checkout to a tested revision. From your private project, add the
framework and only the providers your host uses, with replacements in your own
`go.mod`. For an FS-backed host:

```sh
go mod init private.example/my-agent
go mod edit -require=github.com/defos-ai/commander/framework@v0.1.0
go mod edit -replace=github.com/defos-ai/commander/framework=/absolute/path/to/commander/framework
go mod edit -require=github.com/defos-ai/commander/providers/fs@v0.1.0
go mod edit -replace=github.com/defos-ai/commander/providers/fs=/absolute/path/to/commander/providers/fs
```

`v0.1.0` is the prepared release version, resolved here by local replacements.
Add replacements for each additional Commander module you use, including its
Commander dependencies; a dependency's own replacements do not apply to your
project. The repository's `go.work` supplies local modules when developing inside
the Commander checkout. Follow the [constructor](web/web_agent.go),
[provider factory](web/cli/factory.go) and [host lifecycle](web/cli/cli.go)
to add your actions and entrypoint, then run:

```sh
GOWORK=off go mod tidy
GOWORK=off go build ./...
GOWORK=off go test -race ./...
```

Connect to the controller's bus and artifact storage. Your source stays private;
Commander receives action metadata, work and results. CI needs the pinned checkout
and matching replacement paths. These commands use your project's `go.mod`
independently of any surrounding workspace.

### Run tests

From the repository root, replacing the path for your agent:

```sh
go test -race -count=1 ./apps/agents/web/...
go vet ./apps/agents/web/...
```

See the [testing guide](../../framework/tests/README.md) for wider integration tests.

# Agents

## Overview

Agents advertise actions, execute controller-selected tasks and return results
and artifacts. They can propose follow-up actions; the controller applies mission
rules and Commander handles open decisions.

The standalone [web agent](https://github.com/defos-ai/commander-web-agent) discovers pages and checks links, SEO and
technology signals over HTTP. The [browser agent](https://github.com/defos-ai/commander-browser-agent) adds rendered
accessibility checks and screenshots. See the [architecture](../../docs/overview.md) for component
boundaries and [action contracts](../../framework/README.md) for shared interfaces.

The [builder agent](builder/README.md) starts agent creation by checking out the
example template into an isolated workspace. Build and deployment actions are
still to come.

The [agent package contract](../../docs/agent-packages.md) describes
versioned binaries/images and bootstrap for agents created from
`commander-example-agent`. The [spawner module](spawner/README.md) resolves these
packages and starts native processes or Docker containers through `spawn-agent`
or repeatable `--spawn-agents` flags. Compose and all-in-one start builder and
spawner agents. Ask in chat to install other agents when needed, for example:
**Install the web agent, then review defos.ai.**

## Quick start

Start the [controller](../controllers/README.md#quick-start) first. With Go 1.26.7+,
run from the repository root using the same bus and artifact storage:

```sh
go run ./apps/agents/spawner/cmd --spawn-agents github.com/defos-ai/commander-web-agent@v0.2.0
```

See the [browser agent](https://github.com/defos-ai/commander-browser-agent)
for native requirements and [deployment](../../docs/deployment.md) for configuration.

## Development and testing

### Add an agent

Start from [commander-example-agent](https://github.com/defos-ai/commander-example-agent)
in your own repository. Set its module path and fixed agent type, add your actions,
and update its mission and tests. The template includes provider factories, host
lifecycle, Docker setup, and native/image release workflows.

Its [constructor](https://github.com/defos-ai/commander-example-agent/blob/main/agent.go)
supplies the action catalog and artifact storage to `agent.NewAgent`; its
[provider factory](https://github.com/defos-ai/commander-example-agent/blob/main/cli/factory.go)
connects the bus and storage. See [AGENTS.md](AGENTS.md) for Commander contracts.

### Add an action to an existing agent

Use [`action.NewFunc`](../../framework/agent/action/func.go) for typed JSON
input/output; it derives schemas from your Go types. For file attachments,
implement [`action.Executor`](../../framework/agent/action/action.go) directly.
Declare the action's name, implement its behavior, then
add its constructor to the [action catalog](https://github.com/defos-ai/commander-example-agent/blob/main/agent.go).
[Analyze-text](https://github.com/defos-ai/commander-example-agent/blob/main/actions/analyze_text.go) is a small JSON action example.

### Action readiness

Install required programs and supply settings at deployment. Missing dependencies
make an action unavailable; other actions can still serve. See
[browser discovery](https://github.com/defos-ai/commander-browser-agent/blob/v0.2.0/actions/browser.go) for an implementation and
[mission preflight](../../docs/abstractions.md#discovery-and-preflight) for how
readiness affects scheduling. Each host's `--actions` flag selects its exposed
catalog.

Actions can declare persistent secret names in `Action.RequiredSecrets`.
Secrets belong to the registered agent type, so its instances share the same
named secrets. Declarations remain visible when an action is ready.
For an agent that needs managed secrets, configure `AgentConfig.Secrets` with a
decryptor. The [builder host](builder/README.md#secrets) supports this configuration;
the standalone web and browser hosts do not need it.

Use `action.Secret(ctx, name)` in discovery or execution
to obtain a declared value. Missing or expired values block only the affected
actions. See [secret delivery and refresh](../../framework/secrets/README.md#agent-requirements).

### Return files

Set `Result.Artifacts` to grouped files supplied as bytes or local paths. The
runtime uploads them and records their metadata. See
[capture-page](https://github.com/defos-ai/commander-browser-agent/blob/v0.2.0/actions/capture_page.go), which returns desktop/mobile
PNGs under one artifact.

### Propose follow-up work

Use `FuncConfig.NextActions` or `Result.NextActions` to propose an action name,
JSON input and optional reason. [Discover-pages](https://github.com/defos-ai/commander-web-agent/blob/v0.2.0/actions/discover_pages.go)
proposes five checks per page. The controller queues valid proposals by default;
mission rules can require model review or collect child results. See
[continuations and scheduling](../../docs/abstractions.md#next-actions-and-continuations).

### Develop an agent in a private repository

Imports use repository paths such as `github.com/defos-ai/commander/framework`.
The template pins published Commander dependencies. To develop against an
unreleased Commander checkout, pin that checkout to a tested revision and add
local replacements in your private project's `go.mod`. For an FS-backed host:

```sh
go mod init private.example/my-agent
go mod edit -require=github.com/defos-ai/commander/framework@v0.1.0
go mod edit -replace=github.com/defos-ai/commander/framework=/absolute/path/to/commander/framework
go mod edit -require=github.com/defos-ai/commander/providers/fs@v0.1.0
go mod edit -replace=github.com/defos-ai/commander/providers/fs=/absolute/path/to/commander/providers/fs
```

`v0.1.0` is resolved here by local replacements.
Add replacements for each additional Commander module you use, including its
Commander dependencies; a dependency's own replacements do not apply to your
project. The repository's `go.work` supplies local modules when developing inside
the Commander checkout. Follow the template's [constructor](https://github.com/defos-ai/commander-example-agent/blob/main/agent.go),
[provider factory](https://github.com/defos-ai/commander-example-agent/blob/main/cli/factory.go)
and [host lifecycle](https://github.com/defos-ai/commander-example-agent/blob/main/cli/cli.go)
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

From your agent's repository:

```sh
go test -race -count=1 ./...
go vet ./...
```

See the [testing guide](../../framework/tests/README.md) for wider integration tests.

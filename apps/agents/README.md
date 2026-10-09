# Agents

## Overview

Agents advertise actions, execute controller-selected tasks and return results
and artifacts. They can propose follow-up actions; the controller applies mission
rules and Commander handles open decisions.

The bundled [browser agent](browser/README.md) checks websites and captures
browser screenshots. Its no-browser mission uses HTTP checks; its with-browser
mission also renders pages. See the [architecture](../../docs/overview.md) for component
boundaries and [action contracts](../../framework/README.md) for shared interfaces.

## Quick start

With Go 1.26.7+, run from the repository root:

```sh
go run ./apps/agents/browser/cmd
```

Start the [controller](../controllers/README.md#quick-start) with matching bus and
artifact storage settings. See [native requirements](browser/README.md#native-agent)
and [deployment](../../docs/deployment.md) for configuration.

## Development and testing

### Add an agent

Create `apps/agents/<agent-type>` with the module path
`github.com/defos-ai/commander/apps/agents/<agent-type>`, using the browser agent's
[go.mod](browser/go.mod), [constructor](browser/browser_agent.go) and
[CLI](browser/cli/cli.go) as a starting point. Register it in the workspace:

```sh
go work use ./apps/agents/<agent-type>
```

Supply your agent type, action catalog, permission grants and artifact storage to
`agent.NewAgent`. The [provider factory](browser/cli/factory.go) shows how to
connect it to the bus and storage. For repository coding conventions, see
[AGENTS.md](AGENTS.md).

### Add an action to an existing agent

Use [`action.NewFunc`](../../framework/agent/action/func.go) for typed JSON
input/output; it derives schemas from your Go types. For file attachments,
implement [`action.Executor`](../../framework/agent/action/action.go) directly.
Declare the action's name and required permissions, implement its behavior, then
add its constructor to the [action catalog](browser/actions/actions.go).
[Check-SEO](browser/actions/check_seo.go) is a small JSON action example.

### Action readiness

Install required programs and supply settings at deployment. Missing dependencies
or permissions make an action unavailable; other actions can still serve. See
[browser discovery](browser/actions/browser.go) for an implementation and
[mission preflight](../../docs/abstractions.md#discovery-and-preflight) for how
readiness affects scheduling. The browser host's `--actions` flag selects its
exposed catalog.

### Return files

Set `Result.Artifacts` to grouped files supplied as bytes or local paths. The
runtime uploads them and records their metadata. See
[capture-page](browser/actions/capture_page.go), which returns desktop/mobile
PNGs under one artifact.

### Propose follow-up work

Use `FuncConfig.NextActions` or `Result.NextActions` to propose an action name,
JSON input and optional reason. [Discover-pages](browser/actions/discover_pages.go)
proposes five checks per page. Mission rules authorize automatic transitions;
other proposals wait for Commander. See
[continuations and scheduling](../../docs/abstractions.md#next-actions-and-continuations).

### Develop an agent in a private repository

Imports use repository paths such as `github.com/defos-ai/commander/framework`.
No releases are published yet, so keep your agent in its own module and pin a
Commander checkout to a tested revision. From your private project, add the
framework and only the providers your host uses, with replacements in your own
`go.mod`. For an FS-backed host:

```sh
go mod init private.example/my-agent
go mod edit -require=github.com/defos-ai/commander/framework@v0.0.0
go mod edit -replace=github.com/defos-ai/commander/framework=/absolute/path/to/commander/framework
go mod edit -require=github.com/defos-ai/commander/providers/fs@v0.0.0
go mod edit -replace=github.com/defos-ai/commander/providers/fs=/absolute/path/to/commander/providers/fs
```

`v0.0.0` is an unreleased placeholder resolved by those local replacements.
Add replacements for each additional Commander module you use, including its
Commander dependencies; a dependency's own replacements do not apply to your
project. The repository's `go.work` supplies local modules when developing inside
the Commander checkout. Follow the [constructor](browser/browser_agent.go),
[provider factory](browser/cli/factory.go) and [host lifecycle](browser/cli/cli.go)
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
go test -race -count=1 ./apps/agents/browser/...
go vet ./apps/agents/browser/...
```

See the [testing guide](../../framework/tests/README.md) for wider integration tests.

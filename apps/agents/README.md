# Agents

## Overview

Agents advertise actions, execute controller-selected tasks and return results
and artifacts. They can propose follow-up actions; the controller applies mission
rules and Commander handles open decisions.

The bundled [web quality agent](web-quality/README.md) checks websites and captures
browser screenshots. See the [architecture](../../docs/overview.md) for component
boundaries and [action contracts](../../framework/README.md) for shared interfaces.

## Quick start

With Go 1.26.7+, run from the repository root:

```sh
go run ./apps/agents/web-quality/cmd
```

Start the [controller](../controllers/README.md#quick-start) with matching bus and
artifact storage settings. See [native requirements](web-quality/README.md#native-agent)
and [deployment](../../docs/deployment.md) for configuration.

## Development and testing

### Add an agent

Create `apps/agents/<agent-type>` as a Go module, using the web quality agent's
[go.mod](web-quality/go.mod), [constructor](web-quality/web_quality_agent.go) and
[CLI](web-quality/cli/cli.go) as a starting point. Register it in the workspace:

```sh
go work use ./apps/agents/<agent-type>
```

Supply your agent type, action catalog, permission grants and artifact storage to
`agent.NewAgent`. The [provider factory](web-quality/cli/factory.go) shows how to
connect it to the bus and storage. For repository coding conventions, see
[AGENTS.md](AGENTS.md).

### Add an action to an existing agent

Use [`action.NewFunc`](../../framework/agent/action/func.go) for typed JSON
input/output; it derives schemas from your Go types. For file attachments,
implement [`action.Executor`](../../framework/agent/action/action.go) directly.
Declare the action's name and required permissions, implement its behavior, then
add its constructor to the [action catalog](web-quality/actions/actions.go).
[Check-SEO](web-quality/actions/check_seo.go) is a small JSON action example.

### Action readiness

Install required programs and supply settings at deployment. Missing dependencies
or permissions make an action unavailable; other actions can still serve. See
[browser discovery](web-quality/actions/browser.go) for an implementation and
[mission preflight](../../docs/abstractions.md#discovery-and-preflight) for how
readiness affects scheduling. The web quality host's `--actions` flag selects its
exposed catalog.

### Return files

Set `Result.Artifacts` to grouped files supplied as bytes or local paths. The
runtime uploads them and records their metadata. See
[capture-page](web-quality/actions/capture_page.go), which returns desktop/mobile
PNGs under one artifact.

### Propose follow-up work

Use `FuncConfig.NextActions` or `Result.NextActions` to propose an action name,
JSON input and optional reason. [Discover-pages](web-quality/actions/discover_pages.go)
proposes five checks per page. Mission rules authorize automatic transitions;
other proposals wait for Commander. See
[continuations and scheduling](../../docs/abstractions.md#next-actions-and-continuations).

### Develop an agent in a private repository

Commander modules are not published yet: imports use local names such as
`commander/framework`. Keep your agent in its own module, pin a Commander checkout
to a tested revision, and add local replacements from your private project:

```sh
go mod init private.example/my-agent
go mod edit -require=commander/framework@v0.0.0
go mod edit -replace=commander/framework=/absolute/path/to/commander/framework
go mod edit -require=commander/providers/fs@v0.0.0
go mod edit -replace=commander/providers/fs=/absolute/path/to/commander/providers/fs
```

`v0.0.0` is a local placeholder, not a release. Add replacements for any other
providers you use. Follow the [constructor](web-quality/web_quality_agent.go),
[provider factory](web-quality/cli/factory.go) and [host lifecycle](web-quality/cli/cli.go)
to add your actions and entrypoint, then run:

```sh
go mod tidy
go build ./...
go test -race ./...
```

Connect to the controller's bus and artifact storage. Your source stays private;
Commander receives action metadata, work and results. CI needs the pinned checkout
and matching replacement paths.

### Run tests

From the repository root, replacing the path for your agent:

```sh
go test -race -count=1 ./apps/agents/web-quality/...
go vet ./apps/agents/web-quality/...
```

See the [testing guide](../../framework/tests/README.md) for wider integration tests.

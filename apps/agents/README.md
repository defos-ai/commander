# Agents

## Overview

Agents advertise actions, execute controller-selected tasks and report work,
results and artifacts. Each running instance has its own ID, readiness and `work`
snapshot. Tasks and execution records hold history. Agents can propose follow-up
actions; the controller applies mission rules and Commander handles open decisions.

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

Use the [web quality agent](web-quality/) as a reference:

```text
apps/agents/<agent-type>/
  go.mod          Module and dependencies
  agent.go        Constructor and fixed agent type
  actions.go      Catalog of fresh action instances
  actions/        Action implementations
  cli/            Configuration, providers and lifecycle
  cmd/main.go     Entrypoint
  docker/         Image and external dependencies
  tests/          Behavior tests
```

1. Create module `commander/apps/agents/<agent-type>`. Follow the local dependencies
   in [go.mod](web-quality/go.mod), keeping only providers you use, then run
   `go work use ./apps/agents/<agent-type>` from the repository root.
2. Call `agent.NewAgent` with a fixed `Type`, responsibilities, fresh actions,
   explicit permission grants and an `ArtifactFileStore`. An omitted ID is generated;
   hosts needing it for logging can call `id.AgentID("<agent-type>")`. Generate a new
   ID per process. See [the constructor](web-quality/web_quality_agent.go).
3. Build providers in `cli/factory.go` and pass interfaces to the host. Call
   `worker.Serve(ctx, bus, nil)`, cancel on shutdown, wait for it to return and close
   resources. The [factory](web-quality/cli/factory.go) and
   [CLI](web-quality/cli/cli.go) show this lifecycle.
4. Add behavior tests and include the module in CI; add an image/Compose service
   when needed. Shared runtime changes belong in the framework.

### Add an action to an existing agent

`domain/actions.Action` describes a name, schemas, permissions and readiness.
`agent/action.Executor` implements `Action()`, `Discover(...)` and `Run(...)`.
Use `action.NewFunc` for typed JSON input/output; implement `Executor` directly
for file attachments.

For example, `actions/echo.go`:

```go
package actions

import (
    "context"

    "commander/framework/agent/action"
    actionmodel "commander/framework/domain/actions"
)

type EchoInput struct {
    Text string `json:"text"`
}

type EchoOutput struct {
    Text string `json:"text"`
}

func NewEcho() (action.Executor, error) {
    return action.NewFunc(action.FuncConfig[EchoInput, EchoOutput]{
        Action: actionmodel.Action{
            Name: "echo", Description: "Return the supplied text",
        },
        Run: func(ctx context.Context, input EchoInput) (EchoOutput, error) {
            return EchoOutput{Text: input.Text}, ctx.Err()
        },
    })
}
```

Add the constructor to your catalog, create fresh instances for each agent, and
propagate constructor errors to startup. The helper infers JSON schemas from the
Go types. Callbacks must respect cancellation. Declare `RequiredPermissions` and
have the host grant them explicitly; echo needs none. Add input/output and failure
coverage under the agent's `tests/` folder.

### Action readiness

Actions discover executables through `environment.Environment.FindExecutable` and
configuration through `LookupEnv`. Install programs and supply settings at deployment;
action-specific configuration stays inside the action. The web quality host's
`--actions` flag selects its exposed catalog.

For a missing setting, an executor embedding `action.Base` can report:

```go
a.SetUnavailable("YOUR_KEY is not configured", actionmodel.Requirement{
    Kind: actionmodel.ConfigurationRequirement, Name: "YOUR_KEY", Secret: true,
})
return action.Unavailable, nil
```

Use `ExecutableRequirement` for a missing program and `SetStatus(action.Ready)`
when ready. Returning unavailable lets other actions serve. The runtime reports
missing permission grants. Requirement names feed
[mission preflight](../../docs/abstractions.md#discovery-and-preflight); never expose
secret values in metadata or logs. Recheck credentials during execution and
rediscover after dependencies change.

### Return files

Set `Result.Artifacts` to `[]action.Artifact`. Each artifact groups named
`artifacts.FileSource` entries supplied as `Data` bytes or a local `Path`.
The agent uploads files through `ArtifactFileStore`; the controller saves metadata
in its database. Keep JSON output compact and put bulk data in files.
[Capture-page](web-quality/actions/capture_page.go) returns desktop/mobile PNGs
under one artifact.

### Propose follow-up work

Use `FuncConfig.NextActions` or set `Result.NextActions` in a direct executor.
Each proposal contains an `ActionName`, JSON `Input` matching that action's schema,
and optional `Reason`. See [discover-pages](web-quality/actions/discover_pages.go):
for each page it proposes five checks using `{"url":"https://defos.ai"}` inputs.

Keep proposal generation local and deterministic: malformed proposals or callback
errors fail the action command. Mission rules authorize automatic transitions;
other proposals wait for Commander. Agents do not invoke follow-ups themselves.
See [continuations and scheduling](../../docs/abstractions.md#next-actions-and-continuations).

### Develop an agent in a private repository

Keep source in your own module and follow the construction/action steps above.
Current imports are local module names such as `commander/framework`, not public
GitHub module paths. Pin a Commander checkout to a tested revision and, from your
private project, add local replacements:

```sh
go mod init private.example/my-agent
go mod edit -require=commander/framework@v0.0.0
go mod edit -replace=commander/framework=/absolute/path/to/commander/framework
go mod edit -require=commander/providers/fs@v0.0.0
go mod edit -replace=commander/providers/fs=/absolute/path/to/commander/providers/fs
```

Import the framework packages shown above plus `commander/framework/agent`.
The filesystem provider supplies `comms.NewFsBus` from
`commander/providers/fs/comms` and `artifacts.NewArtifactFileFsStore` from
`commander/providers/fs/artifacts`. Convert configured URLs with
`fs.FilesystemPath` from `commander/providers/fs` before calling those constructors.
Alternatively, inject your own `comms.Bus` and `artifacts.ArtifactFileStore`.

Both replacements belong in the private project's `go.mod`; `v0.0.0` is a local
placeholder, not a published release. Add actions and `cmd/main.go` using the
[host lifecycle](web-quality/cli/cli.go), then run:

```sh
go mod tidy
go build ./...
go test -race ./...
```

Connect to the controller's bus and artifact storage. Source stays in your private
repository; only action metadata, work and results flow through Commander. CI needs
the pinned checkout and matching replacement paths.

### Run tests

Cover action behavior, permissions, dependency discovery and CLI lifecycle.
From the repository root, replacing the path for your agent:

```sh
go test -race -count=1 ./apps/agents/web-quality/...
go vet ./apps/agents/web-quality/...
```

Follow the [testing conventions](../../framework/tests/README.md#writing-tests).

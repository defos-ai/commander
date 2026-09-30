# Agents

## Overview

Agents advertise actions, execute controller-selected work, and publish results.
Each agent owns its action catalog and runs independently. The bundled
[web quality agent](web-quality/README.md) checks websites and captures browser screenshots.

## Quick start

With Go 1.26.7+, run from the repository root:

```sh
go run ./apps/agents/web-quality/cmd
```

Start the [controller](../controllers/README.md#quick-start) with the same bus and
storage locations. See the [web quality requirements](web-quality/README.md#quick-start)
and [local setup](../../docs/deployment.md) for dependencies and connection settings.

## Development and testing

### Add an agent

Use the [web quality agent](web-quality/) as a reference:

```text
apps/agents/<agent-type>/
  go.mod          Agent-specific module and dependencies
  agent.go        Agent constructor and fixed type
  actions.go      Action catalog
  actions/        Action implementations
  cli/            Configuration and lifecycle
  cmd/main.go     Entrypoint
  docker/         Image and external dependencies
  tests/          Behavior tests
```

1. Create the module `commander/apps/agents/<agent-type>`. Follow the existing
   [go.mod](web-quality/go.mod) for local framework/provider requirements,
   keep only dependencies your agent uses, and add it with
   `go work use ./apps/agents/<agent-type>` from the repository root.
2. Generate an ID with `id.AgentID("<agent-type>")` for `agent.AgentConfig.ID`.
   Construct `artifact.NewArtifactFileFsStore(storageRoot)` for file storage.
3. Construct `agent.NewAgent` with your fixed `Type`, responsibilities, fresh action
   instances, granted permissions, and injected `ArtifactFileStore`.
4. Construct providers in the app's `cli/factory.go` and pass framework interfaces
   to `cli.go`, which calls `worker.Serve(ctx, bus, nil)`. Cancel its context on
   shutdown, wait for it to return, and close resources. The existing
   [factory](web-quality/cli/factory.go) and [CLI](web-quality/cli/cli.go) show the setup.
5. Add the module to CI testing and, if needed, add its image/service to Compose.

Follow the [dependency boundaries](../../docs/overview.md#dependencies). Shared runtime
changes belong in the [framework](../../framework/README.md).

### Add an action to an existing agent

`actions.Action` in `framework/domain/actions` is the shared model: name, schemas,
permissions, and readiness. `action.Executor` in `framework/agent/action` supplies
`Action()`, `Discover(...)`, and `Run(...)` for execution on an agent.

1. Add an action under the agent's `actions/` folder. `action.NewFunc` derives JSON
   schemas from Go types; implement `action.Executor` directly when you need attachments.
2. Add its constructor to the agent's catalog, such as
   [`NewActions`](web-quality/actions.go). Create fresh instances for each agent
   and propagate constructor errors through the catalog and CLI.
3. Declare required permissions and grant them explicitly in the host. Discover
   external dependencies and read configuration inside the action through
   `environment.Environment.LookupEnv`. Install executables and supply environment
   variables at deployment. Keep credentials out of public metadata and logs.
4. Add behavior tests under the agent's `tests/` folder.

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

Callbacks must respect context cancellation. This echo action needs no permissions;
network or process actions must declare theirs.

### Action readiness

Implement `Discover` to check executables with `Environment.FindExecutable` and
settings with `Environment.LookupEnv`. For a missing configuration key, call:

```go
Base.SetUnavailable("YOUR_KEY is not configured", actionmodel.Requirement{
    Kind: actionmodel.ConfigurationRequirement, Name: "YOUR_KEY", Secret: true,
})
```

Return `action.Unavailable, nil`; the agent can still serve its other actions. Use
`ExecutableRequirement` for missing programs. Missing permission grants are reported
by the agent runtime. These names feed [mission preflight](../../docs/abstractions.md#discovery-and-preflight);
never include configuration values. Mark a
configured action with `Base.SetStatus(action.Ready)`. Configuration errors should
name the missing setting without including its value. Recheck needed credentials
when running, and rediscover actions after supplying missing dependencies.

The web quality host's `--actions` flag selects which actions to expose. Actions own
their specific settings; adding an action should not require new CLI fields.

### Return files

Set `Result.Artifacts` to `[]action.Artifact`. Each artifact groups named
`artifact.FileSource` entries, supplied as `Data` bytes or a local `Path`.
The agent uploads files through `ArtifactFileStore` and announces their metadata;
the controller saves that metadata in its database. Files stay in shared storage.
See [capture-page](web-quality/actions/capture_page.go) for desktop/mobile images
under one artifact. Keep action output as a compact JSON summary and place bulk
data in artifact files.

### Propose follow-up work

Set `NextActions` in `action.FuncConfig` to derive suggestions from a successful
action output. For example, a page discovery action with a `Pages []string` output can use:

```go
NextActions: func(ctx context.Context, input DiscoverInput, output DiscoverOutput) ([]action.NextAction, error) {
    actions := make([]action.NextAction, 0, len(output.Pages))
    for _, page := range output.Pages {
        raw, err := json.Marshal(map[string]string{"url": page})
        if err != nil {
            return nil, err
        }
        actions = append(actions, action.NextAction{
            ActionName: "check-links", Input: raw, Reason: "Check links on the discovered page",
        })
    }
    return actions, ctx.Err()
},
```

Use your catalog's actual action name and input schema; this example also needs
`encoding/json`. Direct `action.Executor` implementations set `Result.NextActions`.
Malformed proposals or callback errors fail the action command, so keep proposal
generation local and deterministic. Mission automation rules authorize transitions for particular source and
destination actions; other proposals wait for the model. Agents never invoke
follow-up actions themselves. See
[continuation decisions and scheduling](../../docs/abstractions.md#next-actions-and-continuations).

### Develop an agent in a private repository

Keep your agent source in your own module and use the same construction and action
steps above. Current module names are `commander/framework` and
`commander/providers/fs`; use a local Commander checkout pinned to a tested revision.

From your private project's directory:

```sh
go mod init private.example/my-agent
go mod edit -require=commander/framework@v0.0.0
go mod edit -replace=commander/framework=/absolute/path/to/commander/framework
```

Import `commander/framework/agent`, `commander/framework/agent/action`, and
`commander/framework/domain/actions`. Supply
implementations of `comms.Bus` and `artifactstore.ArtifactFileStore`, or use the bundled
filesystem provider by also adding:

```sh
go mod edit -require=commander/providers/fs@v0.0.0
go mod edit -replace=commander/providers/fs=/absolute/path/to/commander/providers/fs
```

Import `commander/providers/fs/comms` and `commander/providers/fs/artifact`
for `comms.NewFsBus` and `artifact.NewArtifactFileFsStore`. Convert configured URLs
with `fs.FilesystemPath` from `commander/providers/fs` before passing paths to
these constructors. Other provider modules are optional.
Both replacements belong in your project's `go.mod`. `v0.0.0` is a local
placeholder, not a published release. Add your actions and `cmd/main.go`, following
the [framework guide](../../framework/README.md)
and the [host lifecycle example](web-quality/cli/cli.go), then run:

```sh
go mod tidy
go build ./...
go test -race ./...
```

Run your private host against the controller's bus and storage locations. Your
source stays in that repository; the agent advertises actions and returns results
through Commander. Provide the pinned checkout and matching replacement paths in CI.

### Run tests

Cover action input/output, permissions, dependency discovery, and CLI lifecycle.
Follow the shared [testing conventions](../../framework/tests/README.md#writing-tests).

From the Commander repository root (replace the agent path for your new host):

```sh
go test -race -count=1 ./apps/agents/web-quality/...
go vet ./apps/agents/web-quality/...
```

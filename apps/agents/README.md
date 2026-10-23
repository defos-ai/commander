# Agents

Agents give Commander actions it can use in a mission.

| You want to… | Use |
| --- | --- |
| Build, publish and release an agent | [Builder](builder/README.md) |
| Install a published agent | [Spawner](spawner/README.md) |
| Check pages, links and SEO over HTTP | [Web agent](https://github.com/defos-ai/commander-web-agent) |
| Capture screenshots and inspect rendered pages | [Browser agent](https://github.com/defos-ai/commander-browser-agent) |

## Quick start

Compose and all-in-one start builder and spawner automatically. Ask Playground:

> Install the web agent, then review defos.ai.

For a separate host, start the [controller](../controllers/README.md#quick-start),
then run from the repository root with Go 1.26.7+:

```sh
go run ./apps/agents/spawner/cmd
```

Use the controller's `BUS_URL` and `STORAGE_URL`. See
[deployment](../../docs/deployment.md) for other providers.

## Development and testing

### Add an agent

Start with [commander-example-agent](https://github.com/defos-ai/commander-example-agent).
Set your module path and agent type, implement actions, and update its mission
and tests. Follow its README to run and release your agent.

### Add an action to an existing agent

1. Use [`action.NewFunc`](../../framework/agent/action/func.go) for typed JSON
   input/output, or [`action.Executor`](../../framework/agent/action/action.go)
   for file artifacts.
2. Add the action constructor to your agent's catalog in `agent.go`.
3. Test its public input, output and failure behavior.

The template's [analyze-text action](https://github.com/defos-ai/commander-example-agent/blob/main/actions/analyze_text.go)
is a small example. Keep registered actions in `actions/`, supporting code in
`internal/`, and tests in `tests/`.

### Action readiness

Install each action's required programs and configure its settings. Check
readiness in Admin's **Agents** page before running a mission.

For managed secrets, declare names in `Action.RequiredSecrets`, configure
`AgentConfig.Secrets`, and read values with `action.Secret(ctx, name)`. Users
supply values through Playground or Admin. See [secret setup](../../framework/secrets/README.md).

### Return files

Add bytes or local file paths to `Result.Artifacts`. See the browser agent's
[capture-page action](https://github.com/defos-ai/commander-browser-agent/blob/v0.2.0/actions/capture_page.go)
for an example returning screenshots.

### Propose follow-up work

Use `FuncConfig.NextActions` or `Result.NextActions` to return action names,
JSON inputs and optional reasons. Set [mission rules](../../docs/abstractions.md#mission-rules)
when follow-up work needs model review or aggregated results.

### Develop an agent in a private repository

The template uses published Commander dependencies. To use an unreleased local
checkout, add replacements in your own project's `go.mod`:

```sh
go mod edit -require=github.com/defos-ai/commander/framework@v0.1.0
go mod edit -replace=github.com/defos-ai/commander/framework=/absolute/path/to/commander/framework
go mod edit -require=github.com/defos-ai/commander/providers/fs@v0.1.0
go mod edit -replace=github.com/defos-ai/commander/providers/fs=/absolute/path/to/commander/providers/fs
```

Add replacements for every Commander module your project uses, including
transitive dependencies. Pin the checkout revision in CI. Then run:

```sh
GOWORK=off go mod tidy
GOWORK=off go build ./...
GOWORK=off go test -race ./...
```

Connect your agent to the controller's bus and artifact storage.

### Run tests

From your agent repository:

```sh
go test -race -count=1 ./...
go vet ./...
```

See [testing](../../framework/tests/README.md) for integration checks and
[AGENTS.md](AGENTS.md) for implementation contracts.

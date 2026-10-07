# Controllers

## Overview

The controller turns Commander's operation commands into agent work. It runs
mission automation, selects exact agent instances, and saves tasks, execution
results, questions and reports. The bundled [default host](default/) receives
commands through the bus, restores operations after restart and opens no HTTP port.

The [admin UI](../admin/ui/README.md) shows controller health under **Controllers**.
Queued tasks wait for ready agents with capacity. The current deployment uses
**one active controller**; the Commander LLM makes open decisions through MCP. See
[architecture and recovery](../../docs/overview.md#state-and-restart-recovery).

## Quick start

With Go 1.26.7+, run from the repository root:

```sh
go run ./apps/controllers/default/cmd --with-missions ./apps/controllers/default/data/default-missions.json
```

`--with-missions` upserts definitions at each startup: matching names are updated,
other missions are preserved. Without it, the host loads no mission file. Compose
supplies the [example file](default/data/default-missions.json) automatically.
Existing operations retain their saved mission definition.

Start the [MCP adapter](../adapters/README.md#quick-start) and
[agents](../agents/README.md#quick-start) with matching connections. See
[deployment](../../docs/deployment.md) for providers, flags and persistent state.

## Development and testing

Customize host configuration and provider connections in
[`default/cli/`](default/cli/). For a new host, use its module and entrypoint as a
starting point. The [framework](../../framework/README.md) provides coordination
and recovery; [missions](../../docs/abstractions.md#mission-automation) define
automatic action transitions.

### Agent selection

The default is `runner.RoundRobinSelector{}`. To choose agents differently, pass a
[`runner.AgentSelector`](../../framework/controller/runner/agent_selector.go) in
`runner.Config.AgentSelector`. `Select(ctx, selection)` receives the task, eligible
agents, load/capacity and last dispatched agent ID. Return a candidate's ID, or an
empty ID to defer work. Errors and invalid IDs leave the task queued; set
`runner.Config.Logger` for diagnostics.

Report the selector's `Name()` in `controller.RegistrationConfig.AgentSelector`.
The [default host](default/cli/cli.go) shows runtime and registration setup. See
[AGENTS.md](AGENTS.md) for scheduler constraints when extending the runtime.

### Run tests

From the repository root:

```sh
go test -race -count=1 ./apps/controllers/default/...
```

See the [testing guide](../../framework/tests/README.md) for integration coverage.

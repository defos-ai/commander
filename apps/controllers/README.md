# Controllers

## Overview

The controller turns Commander's operation commands into agent work. It runs
mission automation, selects exact agent instances, and saves tasks, execution
results, questions and reports. The bundled [default host](default/) receives
commands through the bus, restores operations after restart and opens no HTTP port.

Each process registers its ID, type, host, version and agent-selection strategy in
the database. It renews a 45-second lease every 10 seconds, reports `ready` after
startup and `stopped` after shutdown. An expired lease appears as `unavailable`.
The [admin UI](../admin/ui/README.md) lists these records under **Controllers**;
registrations remain visible after a process stops. They describe controller
health, not ownership of agents or operations.

Scheduling requires a fresh, ready instance advertising the requested action,
with its dependencies and permission grants satisfied and capacity available
(one task per instance by default). Queued work waits for readiness or capacity;
dispatched work stays bound to its recorded instance. Recovery does not silently
move an execution with an unknown outcome to a replacement agent.

The current deployment uses **one active controller**. The Commander LLM makes
open decisions through MCP; embedded controller AI is future work. See
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

- Host configuration and provider construction live in [`default/cli/`](default/cli/).
- Coordination and recovery belong in the [framework](../../framework/README.md).
  Mission-specific rules belong in [mission automation](../../docs/abstractions.md#mission-automation).
- Another controller host gets its own module, CLI, entrypoint and tests, and
  injects provider interfaces into the runtime.
  Use `controller.Register` with a `controllers.ControllerStore`, mark the
  registration ready after startup, and close it after consumers and the runner.

### Agent selection

Provide a [`runner.AgentSelector`](../../framework/controller/runner/agent_selector.go)
through `runner.Config.AgentSelector`. Its `Select(ctx, selection)` method receives
a task snapshot, eligible agent snapshots, runner-local load/capacity and the last
successfully dispatched agent ID. Return one candidate's ID. An empty ID defers
work; an error or invalid ID is logged and leaves the task queued for retry.
Pass a logger through `runner.Config.Logger` to receive these diagnostics.

The default is `runner.RoundRobinSelector{}`. The runner retains readiness,
permission, capacity, persistence and recovery checks. Report the selector's
`Name()` in `controller.RegistrationConfig.AgentSelector`.

Selection runs serially in the scheduler's owner loop, within its five-second
deadline. Implementations must return promptly and respect cancellation. Slow
remote model decisions need asynchronous scheduling before they can be integrated.

### Run tests

From the repository root:

```sh
go test -race -count=1 ./apps/controllers/default/...
```

Follow the [testing conventions](../../framework/tests/README.md#writing-tests).

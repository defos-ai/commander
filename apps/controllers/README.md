# Controllers

## Overview

The controller turns Commander's operation commands into agent work. It runs
mission automation, selects exact agent instances, and saves tasks, execution
results, questions and reports. The bundled [default host](default/) receives
commands through the bus, restores operations after restart and opens no HTTP port.

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

### Run tests

From the repository root:

```sh
go test -race -count=1 ./apps/controllers/default/...
```

Follow the [testing conventions](../../framework/tests/README.md#writing-tests).

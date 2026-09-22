# Commander framework

## Overview

The framework is the reusable Go library for agent execution, controller
coordination, messaging, and shared domain models. Applications supply their
actions and configuration; providers implement the bus and stores.

See [core abstractions](../docs/abstractions.md) for the domain concepts and the
[architecture overview](../docs/overview.md) for dependencies and execution flow.

## Quick start

Use Go 1.26.7+ and start from a concrete implementation:

- [Agents](../apps/agents/README.md): create an agent, add actions, or work in a private repository.
- [Controllers](../apps/controllers/README.md): host coordination and operation tracking.
- [Adapters](../apps/adapters/README.md): expose Commander to external clients.

Import the framework packages you need and inject infrastructure through their
interfaces. Use the bundled [providers](../providers/README.md) or your own implementations.

## Development and testing

- Put execution behavior in `agent`, coordination in `controller`, and operation
  transitions in `controller/runner`.
- Keep shared models and store interfaces in `domain` or `artifact`; define
  commands and events in `comms/clients`. Update consumers and recovery together.
- Use plural domain packages with singular models and matching stores:
  `agents.Agent` and `agents.AgentStore`, in `agent.go` and `agent_store.go`.
- Add public behavior tests under `framework/tests/`, following the shared
  [testing conventions](tests/README.md#writing-tests). Run affected app tests for
  changes across processes.

From the repository root:

```sh
go test -race -count=1 ./framework/... ./framework/tests/...
go vet ./framework/... ./framework/tests/...
```

Details: [message contracts](comms/messages/README.md),
[message handling](comms/README.md), and [testing](tests/README.md).

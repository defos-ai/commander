# Commander framework

## Overview

The framework provides reusable agent execution, controller coordination, domain
models and messaging contracts. Apps supply actions and host configuration;
providers supply the bus and stores. It requires no built-in LLM or specific database.

Read [core concepts](../docs/abstractions.md) for terminology and
[architecture](../docs/overview.md) for execution flow and dependencies.

## Quick start

Use the repository's Go workspace and start from a concrete implementation:

- [Agent](../apps/agents/README.md): register actions and run an agent, including in a private project.
- [Controller](../apps/controllers/README.md): coordinate operations and mission automation.
- [Adapter](../apps/adapters/README.md): expose operation commands and saved data to clients.

Import the framework packages you need and inject their interfaces. The bundled
[providers](../providers/README.md) are optional, separate modules.
Current module paths use `commander/...`; private projects use the
[local module setup](../apps/agents/README.md#develop-an-agent-in-a-private-repository).

## Development and testing

| Package | Put changes here when… |
| --- | --- |
| `agent` | Changing action execution, discovery or result delivery. |
| `controller` | Changing coordination, scheduling or recovery. |
| `domain` | Defining shared entities, state transitions and store interfaces. |
| `comms` | Changing transport contracts, typed commands/events or consumers. |

Keep providers and apps outside framework dependencies. Use singular entities and
matching stores in plural packages: `agents.Agent`, `agents.AgentStore`. Update
persistence, consumers and recovery together when changing a lifecycle.

Controller hosts can supply an `AgentSelector` to customize which eligible agent
receives a task. See [agent selection](../apps/controllers/README.md#agent-selection).

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./framework/...
go vet ./framework/...
```

Add tests for observable behavior and complex invariants under `framework/tests/`.
See [testing](tests/README.md), [message handling](comms/README.md) and
[wire contracts](comms/messages/README.md) for details.

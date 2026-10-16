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
- [Controller](../apps/controllers/README.md): coordinate operations and apply mission rules.
- [Adapter](../apps/adapters/README.md): expose operation commands and saved data to clients.

Import the framework packages you need and inject their interfaces. The bundled
[providers](../providers/README.md) are optional, separate modules.
The module path is `github.com/defos-ai/commander/framework`. Releases are not
published yet; private projects use the
[local module setup](../apps/agents/README.md#develop-an-agent-in-a-private-repository).

Controller hosts can supply an `AgentSelector` to customize which eligible agent
receives a task. See [agent selection](../apps/controllers/README.md#agent-selection).
They can also supply a `RuleSelector` to choose rules for each new operation;
see [rule selection](../apps/controllers/README.md#rule-selection).

## Reference

- [Messaging](comms/README.md): consumers, inbox lifetimes and delivery limits.
- [Wire contracts](comms/messages/README.md): routes, payloads and result acceptance.
- [Domain interfaces](domain/): entities and storage contracts.
- [Cryptography](crypto/README.md): secret encryption and key configuration.
- [Secret management](secrets/README.md): encrypted storage and versioned updates.
- [Testing](tests/README.md): unit and integration commands.
- [Coding guidance](AGENTS.md): framework boundaries and invariants.

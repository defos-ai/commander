# Commander framework

Build agents, coordinate operations and connect your own clients. Apps provide
host configuration; [providers](../providers/README.md) supply messaging and storage.

## Quick start

Start with the component you want to extend:

- [Agents](../apps/agents/README.md): implement and register actions.
- [Controllers](../apps/controllers/README.md): customize agent selection and mission rules.
- [Adapters](../apps/adapters/README.md): expose commands and queries to clients.

The Go module is `github.com/defos-ai/commander/framework`. Inside this repository,
use [go.work](../go.work); outside it, follow the
[local module setup](../apps/agents/README.md#develop-an-agent-in-a-private-repository).

## Action definitions

Use [`actions.Definition`](domain/actions/definition.go) to describe an action's
name, schemas and secret requirements before implementing it.
[`actions.ResolveSchema`](domain/actions/schema.go) validates explicit object schemas.
For an executable action, use the [agent action helpers](agent/action/).

## Agent package manifests

Render release `agent.yaml` files with
[`manifest.Render`](agent/manifest/manifest.go). Supply your binary checksums and
image digest; see the [package guide](../docs/agent-packages.md#source-template).

## Reference

| You want to… | Read |
| --- | --- |
| Understand missions and rules | [Core concepts](../docs/abstractions.md) |
| Connect a message consumer | [Messaging](comms/README.md) |
| Extend a message contract | [Wire messages](comms/messages/README.md) |
| Encrypt and manage secrets | [Cryptography](crypto/README.md) · [Secrets](secrets/README.md) |
| Run checks | [Testing](tests/README.md) |
| Change framework code | [AGENTS.md](AGENTS.md) |

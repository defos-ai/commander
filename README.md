<p align="center">
  <img src="./docs/cover.png" alt="DEFOS Commander: Mission execution runtime for agents and tools" width="100%">
</p>

DEFOS Commander runs missions across AI agents, tools, and external systems.
Start, guide, and review work from your AI chat. Kubernetes runs workloads, **Commander runs missions.**

## Get started

Try Commander through Playground, an interactive terminal chat with your model.
Docker is the recommended way to get started; Go is also supported.

### With Docker (recommended)

```sh
docker compose up --build -d && docker compose run --build --rm playground
```

Select your model and enter its API key or set the provider's key in your environment before launching.

You can also connect your existing AI client to **`http://localhost:4273/mcp`** after starting Compose.
Client configuration examples for VS Code, Codex, and Cursor are in the [adapter guide](apps/adapters/README.md#quick-start).

### With Go

```sh
go build -o commander ./apps/playground/cmd/commander && ./commander --all-in-one
```

This starts the local hosts for your session. Choose your model,
then try the example shown in chat. See [Playground](apps/playground/README.md)
for configuration.

## Use Commander

Ask your AI to list available missions, start one for your objective, or show its progress and results. For example:

> Run review-website for defos.ai and summarize what should be fixed before release.

Try the same mission with your own website. Your AI can ask for your input as work progresses and present a final report.

## More information

- [Core abstractions](docs/abstractions.md): missions, operations, tasks, and results.
- [Architecture overview](docs/overview.md): components, dependencies, and how work flows.
- [Local setup](docs/deployment.md): Compose and configuration.
- [Playground](apps/playground/README.md): try Commander in a terminal chat with your own model.
- [Framework](framework/README.md): reuse and contribute to the shared library.
- [Agents](apps/agents/README.md): add agents and actions, including in private projects.
- [Controllers](apps/controllers/README.md): run and extend coordination.
- [Adapters](apps/adapters/README.md): connect clients and add interfaces.
- [Providers](providers/README.md): use or implement messaging and storage backends.
- [Development and testing](framework/tests/README.md): build and verify changes.

[AGPL-3.0-only](LICENSE) · [Report a vulnerability](SECURITY.md)

<p align="center">
  <img src="./docs/cover.png" alt="DEFOS Commander: Mission execution runtime for agents and tools" width="100%">
</p>

DEFOS Commander coordinates missions across AI agents, tools, and external systems.

Commander is distributed execution infrastructure for AI agents and actions. Give it a mission, then follow, guide, and review the work through your existing AI chat.

Kubernetes runs workloads. **Commander runs missions.**

Commander powers mission execution in DEFOS Platform, coordinating thousands of cybersecurity agents.

## Get started

Try Commander through Playground, an interactive terminal chat with your model.
Choose either Go or Docker:

### With Go

With Go 1.26.7+:

```sh
go build -o commander ./apps/playground/cmd/commander && ./commander --all-in-one
```

This starts the local hosts for your session. Choose your model,
then try the example shown in chat. See [Playground](apps/playground/README.md)
for configuration.

### With Docker (no Go required)

```sh
docker compose up --build -d && docker compose run --build --rm playground
```

Select your model and enter its API key,
or set the provider's key in your environment before launching.

You can also connect your existing AI client to **`http://localhost:4273/mcp`**
after starting Compose.
Client configuration examples for VS Code, Codex, and Cursor are in the
[adapter guide](apps/adapters/README.md#quick-start).

## Use Commander

Ask your AI to list available missions, start one for your objective, or show
its progress and results. For example:

> Run review-website for defos.ai and summarize what should be fixed before release.

Try the same mission with your own website.

Your AI can ask for your input as work progresses and present a final report.

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

Licensed under [GNU AGPL v3.0 only (AGPL-3.0-only)](LICENSE). Report vulnerabilities through [SECURITY.md](SECURITY.md).

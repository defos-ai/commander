<p align="center">
  <img src="./docs/cover.png" alt="DEFOS Commander: mission execution for agents" width="100%">
</p>

DEFOS Commander runs missions across agents and external systems. Give it an
objective through your AI chat, then follow, guide, and review the work.

Kubernetes runs workloads. **Commander runs missions.**

## How it works

**Commander → Controller → Agent**

- **Commander** — your AI client chooses the mission and what work is needed.
- **Controller** — automates the workflow and assigns tasks to available agents.
- **Agent** — executes actions and returns results and evidence.

## Quick start

From a checkout, launch Playground, the Commander terminal chat.

### Docker (recommended; includes agent dependencies)

```sh
docker compose up --build -d && docker compose run --build --rm playground
```

### Go

With Go 1.26.7+:

```sh
go build -o commander ./apps/playground/cmd/commander && ./commander --all-in-one
```

All-in-one state is removed on exit. See [native setup](apps/playground/README.md#go-one-executable)
for agent dependencies.

Choose a model, provide its API key, and try:

> Review defos.ai and summarize what should be fixed before release.

The included [website review](apps/agents/web-quality/README.md#workflow) checks
links, accessibility, SEO, and technology indicators, and captures screenshots.
Your AI reviews the evidence and produces a report.

Already using an AI client? Connect it to **`http://localhost:4273/mcp`** after
starting Compose. See [MCP setup](apps/adapters/README.md#quick-start).

## Explore the project

| You want to… | Read |
| --- | --- |
| Run and configure Commander | [Setup](docs/deployment.md) · [Playground](apps/playground/README.md) |
| Inspect operations in a browser | [Admin UI](apps/admin/ui/README.md) |
| Build an agent or workflow | [Agents](apps/agents/README.md) · [Missions and automation](docs/abstractions.md) |
| Integrate or extend Commander | [Framework](framework/README.md) · [Controllers](apps/controllers/README.md) · [HTTP API](apps/adapters/api/README.md) · [Providers](providers/README.md) |
| Understand the architecture and limits | [Overview](docs/overview.md) |
| Contribute | [Testing](framework/tests/README.md) · [Coding-agent guide](AGENTS.md) |

[AGPL-3.0-only](LICENSE) · [Report a vulnerability](SECURITY.md)

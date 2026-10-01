<p align="center">
  <img src="./docs/cover.png" alt="DEFOS Commander: mission execution for agents" width="100%">
</p>

DEFOS Commander runs missions across agents and external systems.
Give it an objective through your AI chat, then follow, guide, and review the work.
Kubernetes runs workloads. **Commander runs missions.**

## How it works

**Commander → Controller → Agent**

- **Commander** is the decision maker: your LLM through MCP, guided by you. It chooses the mission and what work is needed.
- **Controller** coordinates the operation, applies mission automation, and decides which agent executes each task.
- **Agent** executes actions and reports its status, current work, results, and evidence.

Your AI can inspect work and ask you for decisions; the controller owns agent assignment.

## Quick start

From a checkout, launch Playground: a terminal chat connected to Commander.
Docker is recommended because it includes the agent's dependencies.

### Docker (recommended)

```sh
docker compose up --build -d && docker compose run --build --rm playground
```

### Go

With Go 1.26.7 or newer:

```sh
go build -o commander ./apps/playground/cmd/commander && ./commander --all-in-one
```

Choose a model and provide its API key. The Go option starts temporary local hosts
for the session. See [Playground](apps/playground/README.md) for configuration.

Then try:

> Review defos.ai and summarize what should be fixed before release.

The included mission automatically checks pages, links, accessibility, SEO and
technology indicators, with screenshots as evidence. Your AI reviews the results
and produces a report. See the [website workflow](apps/agents/web-quality/README.md#workflow).

Already using an AI client? Start Compose and connect it to
**`http://localhost:4273/mcp`** using the [adapter guide](apps/adapters/README.md#quick-start).

## Explore the project

| You want to… | Read |
| --- | --- |
| Understand the design and current limits | [Architecture](docs/overview.md) · [Core concepts](docs/abstractions.md) |
| Configure a local stack | [Setup](docs/deployment.md) · [Playground](apps/playground/README.md) |
| Add an agent or action | [Agents](apps/agents/README.md) |
| Extend coordination or client access | [Controllers](apps/controllers/README.md) · [Adapters](apps/adapters/README.md) |
| Reuse the framework or add a backend | [Framework](framework/README.md) · [Providers](providers/README.md) |
| Verify a change | [Testing](framework/tests/README.md) |

[AGPL-3.0-only](LICENSE) · [Report a vulnerability](SECURITY.md)

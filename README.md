<p align="center">
  <img src="./docs/cover.png" alt="DEFOS Commander: framework and runtime for distributed agents" width="100%">
</p>

**DEFOS Commander is a framework and runtime for building distributed agents and orchestrating complex missions.** Automate predictable steps, delegate decisions to AI or people, and track execution, results, and artifacts in one place.

## Where to use it

Use Commander for complex workflows that a single AI cannot reliably coordinate alone:

- **Cybersecurity:** pen-testing, 2C infrastructure, SOC alert triage, and incident response.
- **Investigations:** collect evidence, correlate sources, and build case timelines.
- **Development:** implement features, test changes, and review code across repositories.
- **Sales:** research prospects, qualify leads, prepare outreach, and update CRM records.
- **IT operations:** diagnose incidents and run approved recovery steps.
- **Data processing:** validate datasets, enrich records, and generate reports.

## Quick start

Each option opens Playground. Choose a model, enter its API key, and ask:

> Review defos.ai

Fresh sessions start with the spawner agent. Ask Commander to run a mission, it checks available agents, installs them providing capabilities to complete a mission.

### Docker

Run all-in-one (AIO), with Chromium and Git available for agents you install:

```sh
docker run --rm -it --name commander -p 127.0.0.1:4275:4275 ghcr.io/defos-ai/commander-aio:stable
```

Admin UI is available at [locally](http://localhost:4275). View credentials with `/credentials` in Playground or run in another terminal:
```
docker exec commander commander credentials api
```

### Binary

Linux (amd64):

```sh
curl -fsSL https://github.com/defos-ai/commander/releases/latest/download/commander_linux_amd64 -o commander && chmod +x commander && ./commander --all-in-one
```

macOS (Apple silicon):

```sh
curl -fsSL https://github.com/defos-ai/commander/releases/latest/download/commander_darwin_arm64 -o commander && chmod +x commander && ./commander --all-in-one
```

Windows (amd64):

```powershell
powershell -c "iwr -UseBasicParsing https://github.com/defos-ai/commander/releases/latest/download/commander_windows_amd64.exe -OutFile commander.exe; .\commander.exe --all-in-one"
```

Other architectures: [Releases](https://github.com/defos-ai/commander/releases/latest).

### Source

Docker Compose ([setup and teardown](docs/deployment.md#local-compose)):

```sh
docker compose up --build --wait && docker compose run --build --rm --no-deps playground
```

Go (terminal only; [build with Admin](apps/playground/README.md#native-one-executable)):

```sh
go build -o commander ./apps/playground/cmd/commander && ./commander --all-in-one --no-admin
```

See [Playground](apps/playground/README.md) for native dependencies and configuration.

## How it works

**Commander -> Controller -> Agent**

- **Commander**, your AI client chooses the mission and what work is needed.
- **Controller**, automates the workflow and assigns tasks to available agents.
- **Agent**, executes actions and returns results and evidence.

## Explore the project

| You want to… | Read |
| --- | --- |
| Run and configure Commander | [Setup](docs/deployment.md) · [Playground](apps/playground/README.md) |
| Inspect operations in a browser | [Admin UI](apps/admin/ui/README.md) |
| Build an agent or workflow | [Agents](apps/agents/README.md) · [Missions and rules](docs/abstractions.md) |
| Integrate or extend Commander | [Framework](framework/README.md) · [Controllers](apps/controllers/README.md) · [HTTP API](apps/adapters/api/README.md) · [Providers](providers/README.md) |
| Understand the architecture and limits | [Overview](docs/overview.md) |
| Contribute | [Contributing](CONTRIBUTING.md) · [Testing](framework/tests/README.md) |
| Participate in the community | [Code of Conduct](CODE_OF_CONDUCT.md) |
| Understand licensing | [AGPL-3.0-only](LICENSE) |
| Report a vulnerability | [Security](SECURITY.md) |

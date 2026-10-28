<p align="center">
  <img src="./docs/cover.png" alt="DEFOS Commander: framework and runtime for distributed agents" width="100%">
</p>

**DEFOS Commander is a framework and runtime** for building distributed agents and orchestrating complex missions.

Automate predictable steps, delegate decisions to AI or people, and track execution, results, and artifacts in one place.

Run missions and operations lasting months, orchestrating agents with different models and levels of intelligence.

What are the use cases for Commander? [Ask ChatGPT](https://chatgpt.com/?q=What%20are%20the%20use%20cases%20for%20defos-ai/commander?)

## Quick start

Each option opens Playground. Choose a model, enter its API key, and ask:

> Review defos.ai

### Docker

Run all-in-one (AIO):

```sh
docker run --rm -it --name commander -p 127.0.0.1:4275:4275 ghcr.io/defos-ai/commander-aio:stable
```

Open [Admin UI](http://localhost:4275). Get the login with `/credentials` in Playground.

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

Type `/admin serve` to start Admin UI, or add `--serve-admin-ui` when launching.

### Source

Docker Compose:

```sh
docker compose up --build --wait && docker compose run --build --rm --no-deps playground
```

Go:

```sh
go build -o commander ./apps/playground/cmd/commander && ./commander --all-in-one
```

See [Playground](apps/playground/README.md) for native dependencies and configuration.

## Explore the project

| You want to… | Read |
| --- | --- |
| Run and configure Commander | [Setup](docs/deployment.md) · [Playground](apps/playground/README.md) |
| Inspect operations in a browser | [Admin UI](apps/admin/ui/README.md) |
| Build an agent or workflow | [Agents](apps/agents/README.md) · [Missions and rules](docs/abstractions.md) |
| Integrate or extend Commander | [Framework](framework/README.md) · [Controllers](apps/controllers/README.md) · [HTTP API](apps/adapters/api/README.md) · [Providers](providers/README.md) |
| Understand the architecture and limits | [Overview](docs/overview.md) |
| Compare agent frameworks | [Comparison](docs/competitors.md) |
| Contribute | [Contributing](CONTRIBUTING.md) · [Testing](framework/tests/README.md) |
| Participate in the community | [Code of Conduct](CODE_OF_CONDUCT.md) |
| Understand licensing | [AGPL-3.0-only](LICENSE) |
| Report a vulnerability | [Security](SECURITY.md) |

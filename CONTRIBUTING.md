# Contributing to Commander

Bug reports, documentation, tests, agents, and code improvements are welcome.
Please follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## Start with a problem

- For bugs, open an issue with reproduction steps, your version or commit, and
  expected versus actual behavior. Remove credentials and private data from logs.
- For larger features or design changes, open an issue first so we can agree on
  scope before you invest in an implementation.
- Small fixes and documentation improvements can go straight to a pull request.

Report vulnerabilities privately using [SECURITY.md](SECURITY.md).

## Work locally

Use the Go version declared in [go.work](go.work). From the repository root:

```sh
go build -o commander ./apps/playground/cmd/commander
./commander --all-in-one --no-admin
```

This source build runs without Admin. See [setup](docs/deployment.md) for Docker Compose,
[agent development](apps/agents/README.md#development-and-testing) for adding
actions or agents, and [UI development](apps/admin/ui/README.md#development-and-testing)
for frontend and backend setup.

## Check your changes

Run checks for the affected module, for example:

```sh
go build ./framework/...
go vet ./framework/...
```

Replace `./framework/...` with the module you changed. There is no root Go module,
so root-level `go test ./...` will not work. Follow the [testing guide](framework/tests/README.md)
for provider, API/MCP, and integration checks. Shared changes also need checks of
the components that use them. Documentation-only changes need link checks.

## Open a pull request

- Keep changes focused and follow the surrounding code style; format Go with `gofmt`.
- Explain the problem, what changes for users, and how you verified it.
- Add regression coverage for bugs and update usage documentation when behavior changes.
- Include screenshots for visible UI changes and mention checks you could not run.
- Keep secrets, generated output, and runtime data out of the pull request.

CI selects checks based on changed paths. Detailed implementation guidance for
coding assistants lives in the root and component [AGENTS.md](AGENTS.md) files.

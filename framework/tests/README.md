# Development and testing

This is the shared guide for building and testing Commander. For implementation
changes, use the [framework](../README.md), [provider](../../providers/README.md),
or [application](../../docs/overview.md#components) guides.

## Build locally

Use Go 1.26.7+, matching [go.work](../../go.work). From the repository root:

```sh
go build -o bin/commander-web-quality-agent ./apps/agents/web-quality/cmd
go build -o bin/commander-controller ./apps/controllers/default/cmd
go build -o bin/commander-mcp ./apps/adapters/mcp/cmd/commander-mcp
go build -o bin/commander ./apps/playground/cmd/commander
```

See each component's quick start to run [agents](../../apps/agents/README.md#quick-start),
[controllers](../../apps/controllers/README.md#quick-start), or
[adapters](../../apps/adapters/README.md#quick-start). Connection settings and Compose
instructions live in [local setup](../../docs/deployment.md).

## Test locations

| Scope | Tests |
| --- | --- |
| Shared framework behavior | [`framework/tests`](.) |
| Dependency boundaries | [`framework/tests/architecture`](architecture/) |
| Provider contracts | [`providers/tests`](../../providers/tests/) |
| Web quality agent and actions | [`apps/agents/web-quality/tests`](../../apps/agents/web-quality/tests/) |
| Default controller | [`apps/controllers/default/tests`](../../apps/controllers/default/tests/) |
| MCP adapter | [`apps/adapters/mcp/tests`](../../apps/adapters/mcp/tests/) |
| Playground | [`apps/playground/tests`](../../apps/playground/tests/) |

CLI tests build real executables and exercise their public interfaces. Suites use
temporary state and local fixtures; they need no external credentials or Docker.
HTTP tests require loopback sockets and subprocess support. Browser tests require
Chromium/Chrome on `PATH` or at `CHROMIUM_PATH`; they skip when absent. CI supplies
Chrome to run the complete website and Playground workflows.

## Writing tests

Prefer public APIs and external test packages. Mirror the package under test in
its `tests/` directory; keep tests beside production code only when private
helpers require it. Use **Arrange, Act, Assert**:

1. **Arrange:** create isolated inputs, fixtures, and dependencies.
2. **Act:** exercise behavior through its public entrypoint.
3. **Assert:** verify the result or externally visible effect.

Cover behavior changes and regressions, including relevant failures, duplicate
delivery, cancellation, and restart recovery. Use host CLI tests for changes
across processes. Avoid tests that repeat private implementation steps. Bound
readiness waits and register cleanup for processes, sessions, and servers.

## Run locally

There is no root `go.mod`; [go.work](../../go.work) connects the modules. Select
module paths explicitly when running from the repository root.

For focused feedback, replace the path with the affected suite:

```sh
go test -race -count=1 ./framework/tests/controller/...
```

For all modules, matching the [CI workflow](../../.github/workflows/test.yml):

```sh
for module in framework framework/tests providers \
  apps/agents/web-quality apps/controllers/default apps/adapters/mcp apps/playground; do
  go test -race -count=1 "./$module/..." || exit 1
  go vet "./$module/..." || exit 1
done
```

Use `-count=1` for executable workflows: their subprocess builds depend on source
outside the test package that Go's test-result cache may not track.

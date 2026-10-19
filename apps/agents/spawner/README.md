# Spawner

The spawner will start published Commander agents as native processes or Docker
containers. Its first component, [package resolution](packages/), is available:

- Resolve an exact release such as
  `github.com/defos-ai/commander-example-agent@v0.1.0`.
- Validate `agent.yaml` and select a native platform or a Docker image digest.
- Download a native executable and verify its SHA-256 before making it executable.

Use packages from authors you trust. A checksum verifies downloaded bytes against
the manifest; it does not establish whether the program is safe to run.

## Current scope

This is a Go library. The `spawn-agent` action, host CLI, `--spawn-agents`, process
and container launching, registration checks and retry tracking are still to come.
It supports public GitHub releases; private repositories and moving tags such as
`stable` are not supported yet.

Downloads use HTTPS to GitHub release hosts without environment proxies or
deployment credentials. Manifests are limited to 64 KiB and native executables to
512 MiB. Successful downloads belong to the caller; failed downloads are removed.

See the [package contract](../../../docs/agent-packages.md) and
[implementation guide](AGENTS.md).

## Development

From the repository root with Go 1.26.7+:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/spawner/...
go vet ./apps/agents/spawner/...
```

Tests use in-memory HTTP fixtures and need no GitHub credentials, Docker or
external services.

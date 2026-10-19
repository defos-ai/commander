# Working on the spawner

Read the [agent guide](../AGENTS.md), [root guide](../../../AGENTS.md) and
[package contract](../../../docs/agent-packages.md).

## Current boundary

- This module currently contains only package resolution and native downloads.
  Do not claim that it starts agents. Future startup flags and the spawn action
  should reuse this implementation.
- `packages/reference.go` accepts explicit public GitHub releases. Build download
  URLs from validated owner/repository/version components; do not accept arbitrary
  download URLs or moving aliases in task input.
- `packages/manifest.go` validates the complete manifest, including unused modes.
  Reject unsupported schemas, unknown/duplicate keys, aliases, multiple YAML
  documents, scalar coercions, unsafe asset paths and unpinned images. Asset
  basenames must be safe on the current host, excluding device names and trailing
  dots. Revalidate exported structs when selecting/downloading so a caller cannot
  bypass parsing.
- The manifest contains delivery metadata only. Runtime privileges, environment,
  networks, volumes and managed secrets belong to deployment configuration.

## Downloads and ownership

- `packages/resolver.go` uses a dedicated HTTP client. The default transport does
  not inherit proxies, cookies or credentials. Injected transports are trusted
  deployment configuration and must not add credentials. Redirects require HTTPS
  on the explicit GitHub release host list; do not log signed redirect URLs or
  raw HTTP errors/bodies. Preserve context cancellation in errors.
- Bound manifest size/time and executable size/time. `DownloadNative` writes into
  a unique private child directory under the caller's directory. Files remain
  non-executable until their checksum matches and all writes have succeeded.
- The caller owns successful downloads. On failure remove only the directory
  created by that call. There is no persistent cache, process ownership record
  or duplicate-launch guarantee yet.
- The future spawner assigns and saves `COMMANDER_AGENT_ID` before launching.
  Reconcile an existing launch on retry; never start another process with that
  ID. New/replacement launches need fresh IDs. An observed registration is not
  proof of process ownership or action readiness.

## Validation

Keep public API tests in `tests/`, using an external package and deterministic
HTTP transports. Read the [shared test guide](../../../framework/tests/AGENTS.md).
Cover strict manifest validation, redirect restrictions, cancellation, checksum
failures and output cleanup without public network or paid model calls.

```sh
go test -race -count=1 -timeout=3m ./apps/agents/spawner/...
go vet ./apps/agents/spawner/...
```

The `spawner` owner filter in `.github/test-paths.yml` selects its unit check.
Keep `go.work` and CI coverage aligned with dependencies. Release module tagging
discovers this module through the workspace; no spawner image or executable is
published until a host exists.

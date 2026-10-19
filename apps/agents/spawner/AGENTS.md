# Working on the spawner

Read the [agent guide](../AGENTS.md), [root guide](../../../AGENTS.md) and
[package contract](../../../docs/agent-packages.md).

## Current boundary

- `spawner_agent.go` constructs a regular framework agent. The `spawn-agent`
  action and repeatable CLI `--spawn-agents` share `native.Manager`. Keep provider
  construction in `cli/factory.go`. Docker spawning and inclusion in Playground,
  Compose and release images are separate steps.
- Actions get task/command identity from `action.ExecutionFromContext`, supplied
  by the framework runtime. Never accept a spawn key, agent ID, environment,
  command arguments or deployment privileges in model-provided action input.
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
  created by that call. `native.Manager` owns successful child downloads and
  removes them only after shutdown. Preserve records and child working data.
- Native launch records use hashed request filenames and exclusive creation.
  Flush the assigned full agent ID before `Start`; pass only its eight lowercase
  hex characters as `COMMANDER_AGENT_ID_SUFFIX`. The child constructs its full ID
  using its fixed type. An incomplete record, conflicting request or an intent
  from another host must never authorize a new
  launch. Do not adopt by PID: retries use the owned `exec.Cmd`, and a new host
  reports uncertainty. New/replacement launches need fresh IDs.
- Subscribe before starting the child. Match both assigned ID and manifest type;
  a peer registration announcement is not a controller-persistence receipt.
  Return the observed registration's action readiness without claiming every
  action is usable or that readiness stays unchanged after registration.
- Registration observation belongs to the host and drains its inbox even after
  an action stops waiting. Retain the observed result for retries; do not leave
  a pending subscription accumulating unrelated controller events.
- Action cancellation ends the wait, not child ownership. `Close` cancels pending
  work, terminates and awaits owned children before the host closes providers.
  Linux retains the leader PID until its process group is stopped; other
  platforms terminate the direct child. Processes that escape the group are
  outside this guarantee. This is not a sandbox or process-tree supervision.
- Bootstrap uses an explicit environment allowlist and private HOME/USERPROFILE.
  No inherited API/MCP/model credentials or secrets encryption key. Do not log
  child output or persist bootstrap credentials. CLI resolves FS paths before
  children switch to their private working directories.

## Validation

Keep public API tests in `tests/`, using an external package and deterministic
HTTP transports. Read the [shared test guide](../../../framework/tests/AGENTS.md).
Cover strict manifest validation, redirect restrictions, cancellation, checksum
failures, real native registration, identity conflicts and host cleanup without
public network or paid model calls. Keep the child executable fixture under tests.

```sh
go test -race -count=1 -timeout=3m ./apps/agents/spawner/...
go vet ./apps/agents/spawner/...
```

The `spawner` owner filter in `.github/test-paths.yml` selects its unit check.
Keep `go.work` and CI coverage aligned with dependencies. Release module tagging
discovers this module through the workspace; image/native asset publication has
not yet been added to the release chain.

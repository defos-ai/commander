# Working on the spawner

Read the [agent guide](../AGENTS.md), [root guide](../../../AGENTS.md) and
[package contract](../../../docs/agent-packages.md).

## Current boundary

- [agent.go](agent.go) defines `AgentType`, constructs a regular framework agent
  through `NewAgent` and owns the action catalog helpers. Preserve fresh action
  instances and an explicitly empty caller-supplied catalog. The `spawn-agent`
  action and repeatable CLI `--spawn-agents` share `runtimes.Runtime`
  (`Spawn`/`Close`), defined in [runtimes/runtime.go](runtimes/runtime.go).
  [runtimes/result.go](runtimes/result.go) holds their shared result;
  [native.Manager](runtimes/native/manager.go) and
  [docker.Manager](runtimes/docker/manager.go) implement the interface.
- `cli/factory.go` selects and constructs the runtime and providers, owns created
  resources and closes them in reverse order. `cli.Run` uses deployment
  configuration. `cli.RunWithOptions` accepts production runtime/state-directory
  overrides and a readiness callback for embedding. Playground uses these to
  select native mode and session state, including inside Docker AIO. Keep test
  injection in runtime constructors; do not add resolver options to the host.
  Injected runtime clients remain caller-owned; a manager closes its resolver
  only when it created it. Keep concrete runtime
  imports out of `cli/cli.go`. Compose starts this service in Docker mode by default,
  with one browser and two web package entries pinned to `v0.2.0`.
- Standalone runtime settings are environment-only: `SPAWNER_RUNTIME` defaults
  to `native` and `SPAWNER_STATE_DIR` to `./state/spawner`. Read and require
  `SPAWNER_DOCKER_NETWORK` only in Docker mode; native mode ignores it. Reject
  unsupported runtimes explicitly. Do not reintroduce flags for these settings.
- `--agent-id-suffix` overrides `COMMANDER_AGENT_ID_SUFFIX`, including an
  explicitly empty flag. Validate nonempty values with `id.AgentIDFromSuffix`
  before opening providers; empty values generate a fresh host ID. This setting
  identifies the spawner itself; the runtime assigns each child's ID separately.
  The spawner action has no executable discovery requirements or search-path flag.
- Actions get task/command identity from `action.ExecutionFromContext`, supplied
  by the framework runtime. Never accept a spawn key, agent ID, environment,
  command arguments or deployment privileges in model-provided action input.
- `packages/reference.go` accepts explicit public GitHub releases. Build download
  URLs from validated owner/repository/version components; do not accept arbitrary
  download URLs or moving aliases in task input.
- The shared [manifest package](../../../framework/agent/manifest/) validates the
  complete manifest, including unused modes. Keep release version validation in
  that package and use it when parsing package references.
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
  Native children receive `CHROMIUM_PATH` and `CHROMIUM_NO_SANDBOX` when configured
  by the deployment; keep these settings out of Docker bootstrap.
  No inherited API/MCP/model credentials or secrets encryption key. Do not log
  child output or persist bootstrap credentials. CLI resolves FS paths before
  children switch to their private working directories.

## Docker ownership

- `runtimes/docker/sdk.go` wraps the official Docker Engine SDK. Operator settings
  choose the daemon and explicit network; images must be pinned by digest. Pulls
  do not forward registry authentication. Bound requests and omit raw daemon
  errors, response bodies and environment values from diagnostics.
- Inspect actual container configuration before starting or adopting it. Require
  the recorded name, ownership labels, image, network and bootstrap settings,
  plus the constrained host configuration. An image's inherited volumes must not
  bypass the no-mount policy. Never grant socket access to a child.
- Durable create-only intents precede Create. Reconcile lost Create/Start
  responses by inspecting the owned container; a missing container is uncertain,
  not permission to create again. Only a never-started `created` container may
  start. Exited instances need a new request/ID; no automatic restart policy.
- Native and Docker intents share `records/`: changing runtime must not repeat an
  existing request. Docker observation survives action cancellation. A new host
  requires a fresh announcement; cached readiness is an observed snapshot only.
- Hold an OS lock on the Docker state directory for the manager's lifetime.
  Checking `created` and then starting it is not atomic across hosts: two owners
  could restart a quickly exiting container under the same ID. A crash releases
  the lock; never use a stale create-only lock file for host ownership.
- Docker `Close` ends observers, leaving containers and records intact. The factory
  closes Engine and providers afterward. Containers are not Compose-owned services;
  operators stop the spawner, remove only children on that deployment network
  carrying `ai.defos.commander.spawn-owner`, then remove launch state before
  requesting a fresh startup. Never reset records while owned children exist.

## Validation

Keep public API tests in `tests/`, using an external package and deterministic
HTTP transports. Read the [shared test guide](../../../framework/tests/AGENTS.md).
Cover manifest resolution, redirect restrictions, cancellation, checksum
failures, identity conflicts and runtime cleanup without public network or paid
model calls. Keep CLI tests focused on configuration and help; exercise host
startup, registration and spawned-agent execution through API/MCP stack scenarios.
Strict manifest contract tests live in the framework's tests.
Keep the child executable fixture under tests for native runtime edge cases.
Use fake Engine/HTTP transports for Docker outcomes and safety checks; keep live
Docker resource lifecycle in external setup, outside Go tests.

```sh
go test -race -count=1 -timeout=3m ./apps/agents/spawner/...
go vet ./apps/agents/spawner/...
```

The `spawner` owner filter in `.github/test-paths.yml` selects its unit check.
Playground's filter also includes spawner changes. Keep `go.work` and CI coverage
aligned with dependencies. Release module tagging discovers this module through
the workspace. The spawner is embedded in Commander and also ships standalone:
[push-agents.yml](../../../.github/workflows/push-agents.yml) publishes
`commander-spawner-agent`, and [build-binaries.yml](../../../.github/workflows/build-binaries.yml)
builds its six native assets alongside Commander's six. Keep both executables in
the same native build artifacts and checksum list. Image publication returns its
digest to the release parent; `stable` promotion must use that digest only after
every image and native asset has published. Keep these workflows reusable-only;
adding publication support does not make artifacts available in older releases.

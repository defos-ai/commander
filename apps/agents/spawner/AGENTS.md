# Working on the spawner

Read the [agent guide](../AGENTS.md), [root guide](../../../AGENTS.md) and
[package contract](../../../docs/agent-packages.md).

## Current boundary

- [agent.go](agent.go) defines `AgentType`, constructs a regular framework agent
  through `NewAgent` and owns the action catalog helpers. Preserve fresh action
  instances and an explicitly empty caller-supplied catalog. The `spawn-agent`
  action uses `runtimes.Runtime`
  (`Spawn`/`Close`), defined in [runtimes/runtime.go](runtimes/runtime.go).
  [runtimes/result.go](runtimes/result.go) holds its result;
  [native.Manager](runtimes/native/manager.go) and
  [docker.Manager](runtimes/docker/manager.go) implement the interface.
- Default registered `Responsibilities` in `agent.go` own installation guidance;
  action descriptions own their input behavior. MCP and Playground discover this
  metadata instead of maintaining a package list or installation instructions.
  Accept any compatible GitHub package, including packages outside the
  discovery catalog. Preserve caller-supplied responsibilities. Keep mission
  rules in `install-agent`; agent guidance must not bypass controller dispatch.
- `cli/factory.go` selects and constructs the runtime and providers, owns created
  resources and closes them in reverse order. `cli.Run` uses deployment
  configuration. `cli.RunWithOptions` accepts production runtime/state-directory
  overrides and a readiness callback for embedding. Playground uses these to
  select native mode and session state, including inside Docker AIO. Keep test
  injection in runtime constructors; do not add resolver options to the host.
  Injected runtime clients remain caller-owned; a manager closes its resolver
  only when it created it. Keep concrete runtime
  imports out of `cli/cli.go`. Compose starts this agent in Docker mode alongside
  the builder service. Playground/AIO embed this agent in native mode and the
  builder host from the current build. Additional agents start through the bundled
  `install-agent` mission through chat/MCP. CLI startup registers the host;
  it must not launch packages or bypass controller dispatch.
- Standalone runtime settings are environment-only: `SPAWNER_RUNTIME` defaults
  to `native`. In native mode, `SPAWNER_STATE_DIR` defaults to `./state/spawner`
  for downloads and child working directories; Docker mode ignores it and needs
  no local state volume. Read and require
  `SPAWNER_DOCKER_NETWORK` only in Docker mode; native mode ignores it. Reject
  unsupported runtimes explicitly. Do not reintroduce flags for these settings.
- Optional `SECRETS_ENCRYPTION_KEY` configures this host's managed secret exchange.
  Embedded hosts pass `Options.SecretsEncryptionKey`, which takes precedence over
  the environment. Validate the key before opening providers; construct the
  host cipher and optional runtime `SecretBootstrap` from that same key.
  Compose mounts the existing key volume read-only; all-in-one supplies its
  session key without changing the process environment.
  Bootstrap the key only for the exact resolved module
  `github.com/defos-ai/commander/apps/agents/builder@<version>` with manifest type
  `builder-agent`. This trusts published versions of that official module;
  a self-claimed type, catalog entry or visibility never grants access.
  Keep this exception out of the generic environment allowlist and never save
  its key in launch records, output or logs.
- `--agent-id-suffix` overrides `COMMANDER_AGENT_ID_SUFFIX`, including an
  explicitly empty flag. Validate nonempty values with `id.AgentIDFromSuffix`
  before opening providers; empty values generate a fresh host ID. This setting
  identifies the spawner itself; the runtime assigns each child's ID separately.
  The spawner action has no executable discovery requirements or search-path flag.
- Actions get command identity from `action.ExecutionFromContext`, supplied
  by the framework runtime. Use `command:<UUID>` for runtime requests so different
  executions of a task cannot share in-memory child ownership. Standalone
  `Agent.Work` cannot run `spawn-agent`; it has no controller execution.
  Never accept a spawn key, agent ID, environment,
  command arguments or deployment privileges in model-provided action input.
- Catalog lookup belongs to the Commander client. `spawn-agent` accepts the
  `package` URL/reference and optional `visibility` (`public` by default). Never load a
  catalog in the spawner or add catalog configuration to its host. Private input
  uses environment `GITHUB_TOKEN` first, otherwise `action.RequestSecret` within
  the current execution. Keep discovery ready without a token; do not declare
  it as a static requirement for public installs. Clear temporary token bytes
  after spawning. Public input must remain anonymous even with an inherited
  download token. Visibility selects authentication, not authorization or trust.
- The shared [package metadata](../../../framework/agent/packages/) package owns
  reference parsing and discovery catalogs. `ParseSelector` accepts GitHub HTTPS
  URLs or `github.com/owner/repository[/module/path]`, with an optional exact
  `@vMAJOR.MINOR.PATCH` or `@stable`; omitted versions mean stable. `ParseReference`
  remains strict for exact canonical persisted references and download inputs.
  Reject URL credentials, queries, fragments and arbitrary download URLs.
  Spawner's `packages.Resolver.Pin` resolves stable using the repository's promoted
  root Latest version, preserving module paths. A module path selects
  `module/path/vMAJOR.MINOR.PATCH`; standalone packages use the root version tag.
  Missing or invalid Latest metadata must not fall back to another release.
  MCP only lists packages; never load a catalog or accept catalog aliases here.
  Native and Docker runtimes save the canonical selector and exact pin before
  manifest/executable download or image pull. Controller-dispatched actions use
  the execution's `package-selection` checkpoint.
  Reuse the saved pin on retries, including preparation failures, and confirm its
  durability with an identical save before continuing. Fail closed on conflicting
  or incomplete records. The `launch-intent` checkpoint separately fences
  process/container creation and includes the runtime and assigned child ID.
  Require the current execution's command identity and checkpoint capability;
  never fall back to local records or generate a separate startup identity.
  Return the exact package in results. Never save tokens in either checkpoint.
  Version resolution, manifest and executable downloads remain in this module;
  shared metadata code must not import the spawner.
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
  deployment configuration and must not add credentials. `packages.WithGitHubToken`
  supplies a caller-owned token for one spawn context; keep its bytes intact
  until downloads and image pulls finish, then clear them. Never retain it in the resolver,
  package metadata, launch records or child environment. Authenticated downloads
  use GitHub's fixed API endpoints to select a named asset from the exact published
  release; ignore URLs in metadata. Only initial `api.github.com` requests carry
  authorization. All redirects strip credentials and require HTTPS
  on the explicit GitHub release host list; do not log signed redirect URLs or
  raw HTTP errors/bodies or downloaded manifest parser details. Preserve context
  cancellation in errors. The action owns token acquisition;
  the resolver never reads environment variables or requests managed secrets.
- Bound manifest size/time and executable size/time. `DownloadNative` writes into
  a unique private child directory under the caller's directory. Files remain
  non-executable until their checksum matches and all writes have succeeded.
- The caller owns successful downloads. On failure remove only the directory
  created by that call. `native.Manager` owns successful child downloads and
  removes them only after shutdown. Preserve checkpoints and child working data.
- Native action launches wait for controller persistence before `Start`.
  Persist the assigned full agent ID first; pass only its eight lowercase
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
  No inherited API/MCP/model credentials or GitHub token. Only the trusted builder
  bootstrap may add the secrets encryption key. Do not log child output or
  persist bootstrap credentials. CLI resolves FS paths before
  children switch to their private working directories.

## Docker ownership

- `runtimes/docker/sdk.go` wraps the official Docker Engine SDK. Operator settings
  choose the daemon and explicit network; images must be pinned by digest.
  Only pulls whose parsed registry domain is exactly `ghcr.io` may use
  `packages.GitHubToken(ctx)`, through that request's registry auth header.
  Other registries and public installs remain anonymous. Never read host Docker
  login credentials or retain authentication on the Engine, in launch records,
  container configuration or child environments. Bound requests and omit raw daemon
  errors, response bodies and environment values from diagnostics.
- Inspect actual container configuration before starting or adopting it. Require
  the recorded name, ownership labels, image, network and bootstrap settings,
  plus the constrained host configuration. An image's inherited volumes must not
  bypass the no-mount policy. Never grant socket access to a child.
  Verify encryption-key presence and value against the current trusted bootstrap
  on retries; changing or removing the key must fail reconciliation. Keep the
  value out of errors and records. It is present in the trusted builder's Docker
  environment and is therefore visible to Docker administrators.
- Durable create-only intents precede Create. Reconcile lost Create/Start
  responses by inspecting the owned container; a missing container is uncertain,
  not permission to create again. Only a never-started `created` container may
  start. Exited instances need a new request/ID; no automatic restart policy.
- Native and Docker actions share the `launch-intent` checkpoint name.
  Validate the runtime discriminator: changing runtime
  must not repeat an existing request. Docker observation survives action cancellation. A new host
  requires a fresh announcement; cached readiness is an observed snapshot only.
- Checking `created` and then starting it is not atomic across hosts: two owners
  could restart a quickly exiting container under the same ID. Track
  freshly acknowledged Docker intents in manager memory; only that manager can
  start their `created` containers. Another manager may observe a verified
  running container, but cannot start an uncertain intent. A persisted checkpoint
  does not authorize controller redispatch or ownership transfer.
- Docker `Close` ends observers, leaving containers and checkpoints intact. The factory
  closes Engine and providers afterward. Containers are not Compose-owned services;
  operators stop the spawner, remove only children on that deployment network
  carrying `ai.defos.commander.spawn-owner` before removing the network.
  Never reset execution checkpoints while owned children exist.

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
builds its six native assets alongside Commander and builder. Keep all three executables in
the same native build artifacts and checksum list. Agent package publication
attaches `agent.yaml` and native assets to `apps/agents/spawner/vMAJOR.MINOR.PATCH`,
using the image digest and native checksums from the same tested revision.
Image publication returns its digest to the release parent; `stable` promotion
must use that digest only after every image and native asset has published.
Keep these workflows reusable-only;
adding publication support does not make artifacts available in older releases.

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
  [native.Manager](runtimes/native/manager.go),
  [docker.Manager](runtimes/docker/manager.go) and the injected
  [Kubernetes Manager](runtimes/kubernetes/manager.go) implement the interface.
  CLI factories select the runtime; `cli.go` stays independent of concrete clients.
- Results use `RuntimeKind` and one sealed `RuntimeDetails` value:
  `NativeRuntimeDetails`, `DockerRuntimeDetails` or `KubernetesRuntimeDetails`.
  JSON places the selected payload directly under `runtime_details`, with no
  extra variant wrapper or flat identifier aliases. Decoding selects the concrete
  value from `runtime`, rejects unsupported kinds, missing/null/non-object
  payloads and unknown outer or nested fields, and preserves the receiver on
  failure. The advertised action schema validates the matching payload and its
  semantic values; the DTO decoder does not duplicate that validation.
- Default registered `Responsibilities` in `agent.go` own installation guidance;
  action descriptions own their input behavior. MCP and Playground discover this
  metadata instead of maintaining a package list or installation instructions.
  Permit compatible GitHub packages owned by `defos-ai`, including packages
  outside the source list. Preserve caller-supplied responsibilities. Keep mission
  rules in `install-agent`; agent guidance must not bypass controller dispatch.
- `cli/factory.go` selects and constructs the runtime and providers, owns created
  resources and closes them in reverse order. `cli.Run` uses deployment
  configuration. `cli.RunWithOptions` accepts production runtime/state-directory
  overrides and a readiness callback for embedding. Playground uses these to
  select native mode and session state, including inside Docker AIO. Keep test
  injection in runtime constructors; do not add resolver options to the host.
  Injected runtime clients remain caller-owned; a manager closes its resolver
  only when it created it. Keep concrete runtime
  imports out of `cli/cli.go`. Compose starts this agent in Docker mode;
  its builder service is opt-in. Playground/AIO embed only this agent in native
  mode. Additional agents start through the bundled
  `install-agent` mission through chat/MCP. CLI startup registers the host;
  it must not launch packages or bypass controller dispatch.
- Standalone runtime settings are environment-only: `SPAWNER_RUNTIME` defaults
  to `native`. In native mode, `SPAWNER_STATE_DIR` defaults to `./state/spawner`
  for downloads and child working directories; Docker mode ignores it and needs
  no local state volume. Read and require
  `SPAWNER_DOCKER_NETWORK` only in Docker mode; native mode ignores it. Reject
  unsupported runtimes explicitly. Do not reintroduce flags for these settings.
- Managed-secret delivery is always enabled and receives requested values from
  the controller. Do not read or validate `SECRETS_ENCRYPTION_KEY`, mount its
  volume, or forward it to any child, including the official builder.
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
- Source lookup belongs to the Commander client. `spawn-agent` accepts the
  `package` URL/reference and optional `visibility` (`public` by default). Never load
  source definitions in the spawner or add source configuration to its host. Private input
  uses environment `GITHUB_TOKEN` first, otherwise `action.RequestSecret` within
  the current execution. Keep discovery ready without a token; do not declare
  it as a static requirement for public installs. Clear temporary token bytes
  after spawning. Public input must remain anonymous even with an inherited
  download token. Visibility selects authentication, not authorization or trust.
- [`packages.RequireTrustedSource`](packages/trust.go) applies the host's source
  admission policy using the shared parser and an exact case-insensitive GitHub
  owner comparison with `defos-ai`. Keep the check in the action before token
  handling and in `runtimes.PinPackage` before checkpoints, network access or
  runtime effects. Other owners return `ErrSourceApprovalRequired`; operator-only
  approval is not implemented in this step. No input confirmation boolean,
  environment override, source-list entry or claimed agent type can bypass it.
  Shared parsing/resolution remains unrestricted metadata functionality. The
  Source admission does not grant encryption-key access. Do not change
  registration or secret delivery here.
- The shared [package metadata](../../../framework/agent/packages/) package owns
  reference parsing and discovery source definitions. `ParseSelector` accepts GitHub HTTPS
  URLs or `github.com/owner/repository[/module/path]`, with an optional exact
  `@vMAJOR.MINOR.PATCH` or `@stable`; omitted versions mean stable. `ParseReference`
  remains strict for exact canonical persisted references and download inputs.
  Reject URL credentials, queries, fragments and arbitrary download URLs.
  Spawner's `packages.Resolver.Pin` resolves stable using the repository's promoted
  root Latest version, preserving module paths. A module path selects
  `module/path/vMAJOR.MINOR.PATCH`; standalone packages use the root version tag.
  Missing or invalid Latest metadata must not fall back to another release.
  MCP only lists packages; never load source definitions or accept source aliases here.
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
- Use [`action.RunWithProgress`](../../../framework/agent/action/progress.go) for
  sequential package-selection, preparation, launch-reconciliation and registration
  stages. Fixed messages report observations; never include package URLs, child
  configuration, credentials or raw runtime diagnostics. Keep reporting outside
  checkpoint-to-launch critical regions, preserving their existing ordering.
  Log publication failures do not change results or authorize another launch.
  Reporting ends with its action's wait; runtime ownership and late-registration
  observation retain their existing lifetimes.
- The shared [manifest package](../../../framework/agent/manifest/) validates the
  complete manifest, including unused modes. Keep release version validation in
  that package and use it when parsing package references.
  Reject unsupported schemas, unknown/duplicate keys, aliases, multiple YAML
  documents, scalar coercions, unsafe asset paths and unpinned images. Asset
  basenames must be safe on the current host, excluding device names and trailing
  dots. Revalidate exported structs when selecting/downloading so a caller cannot
  bypass parsing.
- The manifest contains delivery metadata and optional declared responsibilities
  and action descriptions. These are publisher-supplied capability claims, not
  current readiness or deployment instructions. Runtime privileges, environment,
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
  No inherited API/MCP/model credentials, GitHub token or encryption key. Do not log child output or
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
  Reject containers containing `SECRETS_ENCRYPTION_KEY`, including on adoption.
  Never include its value in mismatch diagnostics.
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

## Kubernetes runtime

`SPAWNER_RUNTIME=kubernetes` selects the injected Manager. Configuration lives in
`cli/config.go` and `cli/kubernetes.go`; `cli/factory.go` constructs the in-cluster
client before opening providers, then injects the bus and resolver. Reject invalid
deployment settings before startup. Require AMQP/AMQPS and S3; other runtimes ignore
Kubernetes settings. Shutdown closes Manager, client, resolver and bus in that order.

Current building blocks are the [Pod builder](runtimes/kubernetes/pod.go) and
[narrow REST client](runtimes/kubernetes/client.go); they add no Kubernetes SDK
dependency. The client takes an explicit HTTPS API endpoint and injected HTTP
client; authentication remains caller-owned for that constructor.
[`NewInClusterClient`](runtimes/kubernetes/incluster.go) instead uses Kubernetes'
standard service endpoint and fixed mounted ServiceAccount files. CA/hostname
verification, token refresh and owned transport cleanup are internal; do not expose
credential paths, endpoint overrides or TLS switches as settings. It loads no
kubeconfig or environment proxies. Create/Get/Watch/ReleaseGate/Close bound requests and
watches, sanitize errors and add no application retries or watch reconnection.
Gate removal requires the observed Pod UID, resource version and expected
gate.

The builder requires explicit operator configuration for namespace, ServiceAccount,
configuration/credential references, resource requests/limits and positive run-as
UID/GID. The Pod prefix and image-pull Secret references are optional. It creates
one gated `agent` container with a digest-pinned image, `restartPolicy: Never`, no
token automount, volumes or owner references. Read `BUS_URL`/`STORAGE_URL` through
required Secret key references, allow only optional S3 configuration keys from the
ConfigMap, and set only the assigned suffix as literal environment. The compact
request hash, owner labels and full package/image/request annotations preserve
ownership without embedding credentials. Reject the official Builder package
before construction until its separate bootstrap contract exists.

Returned Pod `Spec` and `Status` retain raw JSON. [Inspection](runtimes/kubernetes/inspect.go)
compares admitted configuration against the saved desired Pod, allowing only
reviewed API defaults and equivalent resource quantities. Reject injected execution
configuration, deletion, terminal/restarted containers and changed process identity.
The API server, admission chain and node status remain trusted: scheduling gates
control scheduler placement, not every execution path. Admission must preserve the
gate and must not bind a node or run injected workloads before inspection; detecting
a changed response cannot undo execution that already occurred.

[`Result`](runtimes/result.go) and the [action schema](actions/spawn_agent.go)
reserve `runtime: kubernetes` with `runtime_details` containing `namespace`,
`pod_name` and `pod_uid`. Native details contain only `pid`; Docker details contain
only `container_id`. A result must use the payload matching its runtime. Namespace
and Pod name follow Kubernetes DNS naming limits; Pod UID is an opaque nonempty
string.

Host configuration:

| Setting | Deployment-owned choice |
| --- | --- |
| `SPAWNER_KUBERNETES_NAMESPACE` | Required child Pod namespace |
| `SPAWNER_KUBERNETES_POD_PREFIX` | Optional literal Pod name prefix; defaults to an empty string |
| `SPAWNER_KUBERNETES_SERVICE_ACCOUNT` | Child ServiceAccount, with token automount disabled |
| `SPAWNER_KUBERNETES_CONFIG_MAP` | Child configuration reference in that namespace |
| `SPAWNER_KUBERNETES_SECRET` | Child provider-credential reference in that namespace |
| `SPAWNER_KUBERNETES_IMAGE_PULL_SECRETS` | Optional unique comma-separated existing pull Secret references |

The factory supplies private child defaults: UID/GID 1000, requests `100m/128Mi`
and limits `500m/512Mi`. These are deployment policy, not action input. Authentication
details stay internal. Docker/Kubernetes default registration waits to 45 seconds;
an explicit existing `--registration-timeout` overrides that default.

Use `<prefix><agent-id>` as the Pod name; an empty prefix uses the assigned agent
ID directly. Save the resolved namespace/name in the launch intent; configuration
changes must not rename an existing launch.

- Resource requests/limits, image-pull Secret references and Pod security remain
  deployment settings. Do not add action inputs or manifest fields for them.
  Reference existing Secrets without reading their contents; package-download
  tokens do not automatically become registry credentials or child environment.
  Do not forward the spawner's entire environment or its secrets encryption key.
- [Launch reconciliation](runtimes/kubernetes/spawn.go) saves the complete desired
  Pod in immutable `launch-intent` before Create. [Checkpoint records](runtimes/kubernetes/record.go)
  validate execution/package/configuration identity on recovery; changed configuration
  must fail instead of renaming a launch. Each worker uses `restartPolicy: Never`.
  A new process needs a fresh agent ID. Save the verified Pod UID in separate
  `launch-receipt`, then its first observed running container ID/start time in
  `launch-instance`; never overwrite an existing fact to adopt a replacement.
  Retain positive UID/process observations in host memory even when receipt saves
  fail; retries must not forget a known identity. Orphan receipts without their
  intent, or a running-instance receipt alongside a gated Pod, fail closed.
- Subscribe for registration before releasing the scheduling gate, after the UID
  receipt is durable. Only the Manager that received a successful intent-save
  acknowledgement may activate that gated Pod; gate removal tests UID/resource
  version/name. A saved checkpoint does not transfer activation authority to a
  restarted host. [Registration observation](runtimes/kubernetes/registration.go)
  validates fresh announcements, full assigned ID/type and action metadata.
  [Readiness polling](runtimes/kubernetes/ready.go) requires the same running
  process before returning cloned action metadata;
  registration observation is distinct from controller-persisted acceptance.
- Reconcile uncertain API/checkpoint responses against the saved name, UID,
  ownership and Pod configuration. A missing, replaced or exited Pod must not
  authorize another Create, restart or replacement under the same request. An
  ungated Pod without its durable UID receipt remains uncertain. Observation
  survives action cancellation; a new host requires a fresh announcement.
  `Close` cancels observers and leaves Pods/checkpoints intact. The controller
  decides graceful stop through its existing protocol. Operators own Pod cleanup,
  checking recorded UID and spawn ownership before removal. Draining and managed
  termination/cleanup remain separate future features.
- Ownership must survive spawner replacement. Keep label values within 63
  characters; put full package references and digests in annotations. Distinguish
  dynamic `commander-spawner` ownership from static infrastructure workloads.
  Do not attach children to the spawner Pod with owner references or use
  cross-namespace owner references. Namespace permissions cannot enforce labels.
- Builder support is deferred. Reject the official Builder package before Create
  until its workspace and namespace contract is implemented. Do not extend the shared agent Secret or
  grant additional privileges as an implicit workaround.
- Infrastructure must grant Pod `patch` before deployment can release gates.
  Do not add Secret-read or broader workload permissions. Unit checks need no
  Kubernetes SDK or cluster. Authentication settings and test injection stay out
  of the CLI; constructor injection does not expand action input or deployment privileges.

## Validation

Keep public API tests in `tests/`, using an external package and deterministic
HTTP transports. Read the [shared test guide](../../../framework/tests/AGENTS.md).
The narrow exception is `runtimes/kubernetes/incluster_test.go`: package-local
tests inject private file reads and in-memory TLS connections so credential paths
and transport internals stay unexported. They must not access real account files.
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
builds its six native assets alongside Commander and builder. Keep all three in
the same native build artifacts. Publish standalone agent binaries and their
checksums only in their module-tag package releases; the root release contains
Commander binaries. Agent package publication
attaches `agent.yaml` and native assets to `apps/agents/spawner/vMAJOR.MINOR.PATCH`,
using the image digest and native checksums from the same tested revision.
Image publication returns its digest to the release parent; `stable` promotion
must use that digest only after every image and native asset has published.
Keep these workflows reusable-only;
adding publication support does not make artifacts available in older releases.

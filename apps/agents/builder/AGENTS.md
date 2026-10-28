# Working on the builder agent

Read the [agent guide](../AGENTS.md) and [root guide](../../../AGENTS.md) first.

## Entry points

- [agent.go](agent.go) defines `AgentType` and `NewAgent`, returning the framework
  agent with a fresh catalog when `AgentConfig.Actions` is nil. An explicitly
  empty catalog stays empty. Keep catalog construction here; individual action
  constructors belong in `actions/`.
- [cli](cli/) owns host configuration, provider factories and lifecycle.
  `--workspace-dir` overrides `BUILDER_WORKSPACE_DIR`; the default is
  `state/builder-workspaces`. Providers follow the example-agent template's conventions.
- `--agent-id-suffix` overrides `COMMANDER_AGENT_ID_SUFFIX`, including an explicit
  empty flag. Validate nonempty suffixes with `id.AgentIDFromSuffix` before opening
  providers; empty/unset values generate fresh IDs. Keep the fixed `builder-agent`
  type and never accept full IDs or a caller-supplied type prefix.
- `cli.RunWithOptions` accepts an initialized `Options.SecretCipher` for embedding.
  A non-nil cipher takes precedence over optional `SECRETS_ENCRYPTION_KEY`;
  validate it before opening providers. Keep `Run` as the wrapper with empty
  options. Do not mutate process environment or generate keys in this host.
- [checkout-template](actions/checkout_template.go) accepts `{}` and uses a fixed
  source URL; do not expose paths or arbitrary Git URLs through task input.
- [build-agent](actions/build_agent.go) accepts the checkout's opaque workspace
  ID, instructions and 1–100 `actions.Definition` contracts. Require unique safe
  action names, trimmed nonblank descriptions, compilable input/output schemas
  explicitly declaring `type: object`, and valid unique required secret names.
  Preserve schema extensions and allow local references only. Validate contracts
  before running Codex and include the complete definitions in its prompt.
  Define contracts from the operator's request before checkout and pass the
  mission's instructions and actions unchanged into the build continuation.
  Declared secret names belong to the generated agent, not builder readiness;
  only managed `CODEX_API_KEY` gates this action. It runs Codex and Go
  tests/build, validates the generated release structure and actual registration,
  and exports
  `source.tar.gz` through the normal artifact runtime. Its successful output
  records the generated `agent_type`, `module_path` and `binary` alongside the
  workspace ID and source count.
  Never publish, spawn or accept secret values through this action's input.
- [publish-agent](actions/publish_agent.go) consumes the saved source artifact ID
  and full file descriptor, verifies its bytes, then publishes source to GitHub.
  It needs managed `GITHUB_TOKEN`; keep values out of inputs, errors and subprocess
  arguments. The bundled build mission offers this action only after successful
  build review and operator agreement; declining it must still allow completion
  with the source artifact. `GITHUB_TOKEN` gates publication, not checkout/build.
  Publication has no dependency on a local checkout or builder affinity.
  `BUILDER_DEFAULT_REPO_VISIBILITY` optionally sets `public` or `private`; validate
  it at startup and advertise it as the action schema default. Without an explicit
  visibility or advertised default, clients must ask the operator and wait.
  Recommend Public without selecting it implicitly. Preserve existing visibility,
  reject conflicting history, and never force-push. A source commit is not an
  installable release.
- [release-agent](actions/release_agent.go) is an optional continuation after
  source publication and an explicit operator release request/version. It uses
  managed `GITHUB_TOKEN` through the fixed GitHub API origin, without local Git
  or workspace affinity. Preserve the approved commit and generated agent type.
  Validate the known template workflow before dispatch: its initial prepare
  guard must reject a moved default branch, and its run name carries the
  execution UUID for reconciliation. Checkpoints save immutable intent and a
  dispatch-attempt marker before POST; an uncertain response never authorizes
  another POST for that execution. Reconcile by request ID or inspect GitHub.
  Never automatically retry failed CI or create replacement release tasks.
  Status checks carry `workflow_run_id`, read the same run, and propose another
  check after a bounded wait. They stay outside the mission's model-decision
  rule so pending checks continue automatically. Empty terminal proposals return
  to model review. Verify run identity/version, published stable release, exact
  tag commit, bounded manifest, all six uploaded native asset SHA-256 digests
  and expected digest-pinned image metadata before returning a package reference.
  Do not pull images, download every binary, change visibility or install here.
- The mission and agent responsibilities offer installation only after a verified
  release. Honor an existing explicit choice or ask a linked **Install this agent?**
  question and await the answer; expiry is not approval. Finish the build report
  first, then use the existing `install-agent` mission in a separate operation
  with the released package and visibility unchanged. Review the saved spawn
  result and action readiness before completing installation. Keep this policy
  shared through mission/registration metadata, usable by every MCP client.
  Declining or failing installation does not change the saved build/release outcome.
- Keep registered action implementations in `actions/`. Supporting
  code lives in `internal/build/` (Codex, Go and source archives), `internal/git/`
  (isolated Git commands) and `internal/process/` (executable checks and process
  cancellation). Keep publication support under `internal/` by purpose. Internal
  packages must not import `actions/`.
  [internal/github](internal/github/) owns fixed-origin release API calls and
  safe errors; [internal/releaseworkflow](internal/releaseworkflow/) checks the
  shared template contract. This structural check is not a workflow sandbox.
- [Progress reporting](../../../framework/agent/action/progress.go) uses
  `action.RunWithProgress` for fixed stage names and periodic liveness observations.
  Bound each publication, preserve
  the original work result/error, and cancel and join the reporter when the stage
  ends. Keep messages free of inputs, paths, tool diagnostics and secret values.
  Completion describes the named stage: source archives still need runtime upload
  and controller acceptance, and release request handling may return pending or
  uncertain. Keep checkpoint and side-effect ordering intact.

## Workspace and process boundaries

- Constructors must not clone repositories or require executables. Discovery uses
  `environment.Environment.FindExecutable` and respects explicit scopes, including
  an empty scope. Missing tools make only the affected action unavailable.
- Create a private, unique checkout per execution. Return an opaque workspace ID
  and exact revision, never a host path. Successful checkouts remain in configured
  storage; remove only the current incomplete checkout on errors or cancellation.
- [Git commands](internal/git/command.go) run argv directly with bounded output and an isolated
  environment. Never inherit managed secrets, Git config, credential helpers or
  proxy settings. Disable hooks/templates and recursive submodules. The temporary
  child home is removed after execution. Keep Git errors free of raw remote output.
- Unix cancellation stops the Git process group before cleanup; other platforms
  currently use `exec.CommandContext` cancellation of the direct process.
- Checkout must not run template code, build, publish, or launch generated agents.
  Those stages need their own actions and managed-secret declarations. Do not add
  environment fallbacks for managed secrets.
- Build uses Codex's workspace-write sandbox with temporary configuration and
  no inherited user authentication. Independent Go checks run with `GOWORK=off`
  and no managed key in their environment. They execute generated code as the
  builder's operating-system user; environment isolation is not an OS sandbox.
  Preserve redacted subprocess errors and never return raw model/tool output.
- After ordinary Go tests/build, verify release entry points and reject the
  unchanged example identity/module or checkout-local Go replacements. Reuse the
  template's `.github/scripts/release.go` metadata and manifest commands in the
  clean environment, with a synthetic version, six temporary assets and a
  temporary image digest. The framework owns manifest rendering; do not require
  a repository-local `agent.yaml.tmpl`. Treat tool output as untrusted; bound it and validate
  names before using them as paths. Parse the resulting manifest through
  `framework/agent/manifest` and compare its identity, platforms, asset names,
  checksums and image with the checked metadata. Keep synthetic files out of the
  source archive. Failures preserve the workspace, like failed Go checks.
  This checks release structure only: it does not build every native platform or
  Docker image, verify GitHub permissions, run release CI, or create a release.
  It uses no GitHub token. Actual release publication remains a later action.
  Require nonempty manifest responsibilities and action summaries for newly built
  agents. Preserve pure `Responsibilities()` and `ActionDescriptions()` exports;
  the template's manifest helper evaluates them using a bounded temporary Go
  program, an import derived from the generated module, and a clean environment.
  The built-in package generator uses these same exports and shared action
  description constants. Never derive metadata from discovery or runtime secrets.
- Before exporting source, use a temporary Go probe outside the checkout to call
  the generated module's `NewAgent(agent.AgentConfig)` with explicit host metadata
  and an artifact store that rejects access. Inspect `Describe().Actions`; never
  call discovery, serving or action execution, and never require generated-agent
  secret values. Use exclusive temporary files because Codex can write scratch.
  Project static wire fields rather than trusting release summaries. Match the
  constructed type to the package identity, require the exact requested action set,
  descriptions, input/output schemas and required secret names, and reject stale
  manifest names/descriptions too. Compare schemas structurally without float64:
  ignore object order, whitespace and equivalent numeric spellings; retain array
  order and extension fields. Secret-name and action-list order are insignificant.
  Keep child output bounded to 1 MiB, inspection bounded to two minutes, diagnostics
  redacted and the probe out of artifacts. Failures preserve source and return no
  artifacts. Constructor inspection checks declarations, not implementation behavior.
- Workspace storage is local to the owning builder. The bundled `build-agent`
  mission binds checkout and build with the controller's `same-agent` rule.
  Do not route a workspace to another builder or duplicate affinity in the action.

## Deployment

- [Dockerfile](docker/Dockerfile) includes Git, Go, Codex and CA certificates, runs as
  UID/GID 1000, and creates `/home/commander/workspaces` with matching ownership.
  Keep `BUILDER_WORKSPACE_DIR` aligned with this writable directory.
- [Compose](../../../compose.yaml) starts builder only when its `builder` profile
  or service is selected. It mounts `builder-workspaces` at
  `/home/commander/workspaces`; retain it across container recreation.
  Playground all-in-one does not embed a builder host. Separately spawned builders
  keep local workspaces; package spawning does not provision persistent mounts.
- Compose's secrets-key initializer supplies API, spawner and the optional builder
  through read-only mounts. For explicitly spawned builders, the spawner's trusted
  bootstrap passes the same key to the official package only, checking its exact
  repository/module and `builder-agent` manifest type. Never add the key
  to the shared agent environment or infer trust from type alone. Action secrets
  still use managed-secret delivery; no GitHub or model tokens are inherited.
  Native users supply these tools; both builder and all-in-one Docker images include them.
- The release package uses `apps/agents/builder/<version>` in this repository
  with `agent.yaml`; its reference includes `apps/agents/builder` before
  `@<version>`. It provides six native platforms and Docker.
  The shared manifest renderer must receive the builder image digest from the
  same release run. Package metadata cannot request keys or mounts; the spawner
  owns the narrow trusted-builder key policy. Docker administrators can inspect
  the key in the spawned container environment.

## Validation

Read [shared test guidance](../../../framework/tests/AGENTS.md) for new tests.
Follow the [agent test layout](../AGENTS.md#validation): keep tests and fixtures in
`tests/` and exercise public APIs. Use local Git fixtures; never clone the public
template in tests. Cover discovery, strict input, isolated workspaces,
revision output, config/credential isolation, failure cleanup and cancellation.
Build scenarios use local executable fixtures rather than paid Codex calls;
verify secret isolation, failed validation, release-structure failures and source
artifact contents. Cover successful metadata/manifest checks without including
synthetic release assets in the saved source.

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/builder/...
go vet ./apps/agents/builder/...
```

Keep the builder owner filter and unit step in `.github` aligned with provider
imports, including the downstream Playground filter. Release image publication
uses `push-agents.yml`; `push-binaries.yml` publishes its module-tag package with
`Latest` disabled before the root release and stable promotion. Keep native
builder assets and their manifest checksums tied to the same tested revision.
For deployment changes, run the Compose and release wiring checks from
`.github/tests` with `GOWORK=off go test -run '^TestCI' ./...` and validate
`docker compose config --quiet`. Test the builder image as its configured
non-root user, including workspace creation and `git --version`.

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
- [checkout-template](actions/checkout_template.go) is the only action so far.
  Its input is `{}`. The source URL is fixed; do not expose paths or arbitrary
  Git URLs through task input.

## Workspace and process boundaries

- Constructors must not clone repositories or require Git. Discovery uses
  `environment.Environment.FindExecutable` and respects explicit scopes, including
  an empty scope. Missing or removed Git makes only the action unavailable.
- Create a private, unique checkout per execution. Return an opaque workspace ID
  and exact revision, never a host path. Successful checkouts remain in configured
  storage; remove only the current incomplete checkout on errors or cancellation.
- [git.go](actions/git.go) runs argv directly with bounded output and an isolated
  environment. Never inherit managed secrets, Git config, credential helpers or
  proxy settings. Disable hooks/templates and recursive submodules. The temporary
  child home is removed after execution. Keep Git errors free of raw remote output.
- Unix cancellation stops the Git process group before cleanup; other platforms
  currently use `exec.CommandContext` cancellation of the direct process.
- Checkout must not run template code, build, publish, or launch generated agents.
  Those stages need their own actions and managed-secret declarations. Do not add
  environment fallbacks for managed secrets.
- Workspace storage is local to the owning builder. Controller selection has no
  workspace affinity yet. Add explicit ownership/routing or source transfer before
  proposing multi-step work across builder instances.

## Deployment

- [Dockerfile](docker/Dockerfile) includes Git and CA certificates, runs as
  UID/GID 1000, and creates `/home/commander/workspaces` with matching ownership.
  Keep `BUILDER_WORKSPACE_DIR` aligned with this writable directory.
- [Compose](../../../compose.yaml) and Playground all-in-one start only the
  spawner among agents. Builder starts on demand through `spawn-agent`: Docker
  for Compose and native for all-in-one. Keep checkout workspaces local to the
  spawned builder; package spawning does not provision persistent mounts.
- Compose's secrets-key initializer supplies API and spawner. The spawner's
  trusted bootstrap passes the same key to the official builder package only,
  checking its exact repository/module and `builder-agent` manifest type.
  Playground uses its session key through the same bootstrap. Never add the key
  to the shared agent environment or infer trust from type alone. Action secrets
  still use managed-secret delivery; no GitHub or model tokens are inherited.
  Native users supply Git; both the builder and all-in-one Docker images include Git.
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

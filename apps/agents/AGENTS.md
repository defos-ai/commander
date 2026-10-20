# Working on agent hosts

## Construction and lifecycle

- Use [commander-example-agent](https://github.com/defos-ai/commander-example-agent)
  as the reference for new standalone agents. Its `agent.go`, `actions/`, `cli/`
  and `tests/` separate construction, capabilities, providers and validation.
  Only Commander-owned modules belong in [go.work](../../go.work).
- Give each agent a fixed type and a fresh ID per process. Construct fresh action
  instances for every agent so discovery state is never shared between hosts.
  Propagate constructor failures instead of starting a partial runtime.
- Keep the fixed `AgentType`, `NewAgent` constructor and action catalog in
  `agent.go`. Return the framework's `*agent.Agent` directly when the host adds no
  agent methods or state; individual action implementations belong in `actions/`.
- Keep provider construction in each host's `cli/factory.go`; inject the bus and
  artifact file store into the runtime. Unsupported schemes must fail at startup.
- `Run` accepts cancellation without installing signal handlers; `Main` owns
  process signals. Cancel the host, await its runtime shutdown, then close provider
  clients. Joined transport or persistence errors must survive normal cancellation.
- Reusable hosts also run inside Playground. Keep process exits, global signal
  handlers and UI-specific dependencies out of constructors and reusable `Run` code.

## Package contract

- [Agent packages and bootstrap](../../docs/agent-packages.md) defines the release
  manifest and spawner behavior. The [spawner module](spawner/AGENTS.md)
  implements native/Docker spawning with `spawn-agent` and repeatable
  `--spawn-agents`. Compose uses Docker mode; Playground and Docker AIO embed
  the spawner in native mode. Fresh deployments also start the builder from the
  current build; other agents are requested through the `install-agent` mission
  or explicit startup packages. Keep web/browser examples pinned to `v0.2.0`.
  Web/browser source, unit tests and releases belong to their standalone repositories.
- Keep `commander-example-agent` aligned with the package contract.
  Package workflows must publish binaries, image digest and manifest from the
  same tested revision; keep action schemas and requirements in registration.
- Built-in packages use their module-path references and existing module tags:
  `github.com/defos-ai/commander/apps/agents/<name>@<version>` resolves
  `agent.yaml` from `apps/agents/<name>/<version>` in the Commander repository.
  The manifest version remains the bare version. Builder and spawner each
  provide six native assets and Docker. Keep standalone repository
  references resolving `agent.yaml` from the root version tag. Agent releases
  never become `Latest`; root publication and stable promotion wait for them.
- `--agent-id-suffix` overrides `COMMANDER_AGENT_ID_SUFFIX`, including an explicit
  empty flag. Nonempty suffixes accept exactly eight lowercase hex characters.
  The host combines it with its fixed type using `id.AgentIDFromSuffix` and sets
  `AgentConfig.ID`; reject full IDs, prefixes and invalid suffixes before opening
  providers. Unset/empty values generate fresh IDs. The spawner saves the full
  ID before launch and passes only its suffix to the child. It reconciles retries
  against that instance; a new or replacement launch needs a fresh ID.
  Never start two processes with the same
  ID. This is instance correlation, not authorization or duplicate prevention.
  Keep provider settings, secrets and launch permissions under deployment control.

## Actions and readiness

- Use [action.NewFunc](../../framework/agent/action/func.go) for typed JSON actions;
  use [action.Executor](../../framework/agent/action/action.go) when execution needs
  file artifacts. Keep declared schemas and runtime validation consistent.
- Discover executables and configuration through the
  [environment abstraction](../../framework/agent/environment/). Honor explicit
  discovery scopes. Missing requirements make that action unavailable without
  disabling unrelated actions; report actionable requirement metadata.
- Advertise secret names, never values. Recheck required configuration during
  execution and invalidate stale discovery state when rediscovering dependencies.
- Declare persistent names in `Action.RequiredSecrets`; do not infer them from
  transient `MissingRequirements`. The registered agent type determines the
  secret scope; a type and secret name identify one shared value.
  Configure the framework secret runtime with an injected decryptor only for
  hosts that need managed secrets. Builder and spawner support this configuration.
  Web and browser actions do not need managed secrets; do
  not load or distribute the encryption key to their hosts.
  Access declared values with `action.Secret(ctx, name)`, never action inputs or
  global `os.Setenv`. Keep returned values out of outputs/logs and clear temporary
  byte copies. Missing values gate the affected action even without a decryptor.
- Catalog entries describe capabilities; per-agent readiness and leases
  determine availability. Do not claim that installing one agent guarantees
  complete required-action coverage for a mission.
- Keep HTTP/HTML actions in `commander-web-agent` and rendering actions in
  `commander-browser-agent`. Discovery
  can propose another agent type's actions by name; it must not import or call
  that agent's runtime.

## Results and follow-up work

- Return artifact groups as bytes or local paths. The runtime uploads file bytes
  through `ArtifactFileStore`; the controller accepts metadata through `ArtifactStore`.
  Uploaded bytes alone are not published evidence. Preserve ownership and checksums.
- Agents may return schema-valid next-action proposals with input and a reason.
  Mission rules own automation, and the controller dispatches accepted work; an
  action must never directly execute or send its proposed follow-up commands.
- Keep immutable execution outcomes separate from later operation decisions.
  Cancellation or transport uncertainty must not lead to duplicate execution.

## Validation

Keep tests and fixtures in each agent module's `tests/` directory, using external
test packages to exercise public action and host APIs. Use existing dependency
injection for local fixtures. Do not place tests in `actions/` or `cli/` merely
to access private helpers, or export implementation details and add runtime
settings solely for tests.

Run the affected agent module from the repository root, for example:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/builder/...
go vet ./apps/agents/builder/...
```

[Unit CI](../../.github/workflows/test-unit.yml) covers Commander-owned hosts. Test
discovery failures, readiness and local fixtures without paid models
or public websites. Use [adapter scenarios](../../framework/tests/README.md#mcp-api-tests)
for behavior spanning agents, controller persistence and mission automation.

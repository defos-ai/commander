# Working on agent hosts

## Construction and lifecycle

- Use the [web constructor](web/web_agent.go),
  [host](web/cli/cli.go) and [factory](web/cli/factory.go) as the
  extension points for an agent module. Add new modules to [go.work](../../go.work).
- Give each agent a fixed type and a fresh ID per process. Construct fresh action
  instances for every agent so discovery state is never shared between hosts.
  Propagate constructor failures instead of starting a partial runtime.
- Keep provider construction in each host's `cli/factory.go`; inject the bus and
  artifact file store into the runtime. Unsupported schemes must fail at startup.
- `Run` accepts cancellation without installing signal handlers; `Main` owns
  process signals. Cancel the host, await its runtime shutdown, then close provider
  clients. Joined transport or persistence errors must survive normal cancellation.
- Reusable hosts also run inside Playground. Keep process exits, global signal
  handlers and UI-specific dependencies out of constructors and reusable `Run` code.

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
  Configure the framework secret runtime with an injected decryptor; bundled
  hosts read optional `SECRET_ENCRYPTION_KEY` and validate it before provider startup.
  Access declared values with `action.Secret(ctx, name)`, never action inputs or
  global `os.Setenv`. Keep returned values out of outputs/logs and clear temporary
  byte copies. Missing values gate the affected action even without a decryptor.
- Catalog entries describe capabilities; per-agent readiness and leases
  determine availability. Do not claim that installing one agent guarantees
  complete required-action coverage for a mission.
- Keep HTTP/HTML actions in `web` and rendering actions in `browser`. Discovery
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
go test -race -count=1 -timeout=3m ./apps/agents/browser/...
go vet ./apps/agents/browser/...
```

[Unit CI](../../.github/workflows/test-unit.yml) covers the bundled host. Test
discovery failures, readiness and local fixtures without paid models
or public websites. Use [adapter scenarios](../../framework/tests/README.md#mcp-api-tests)
for behavior spanning agents, controller persistence and mission automation.

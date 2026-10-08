# Working on agent hosts

## Construction and lifecycle

- Use the [web quality constructor](web-quality/web_quality_agent.go),
  [host](web-quality/cli/cli.go) and [factory](web-quality/cli/factory.go) as the
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

## Actions, readiness and permissions

- Use [action.NewFunc](../../framework/agent/action/func.go) for typed JSON actions;
  use [action.Executor](../../framework/agent/action/action.go) when execution needs
  file artifacts. Keep declared schemas and runtime validation consistent.
- Discover executables and configuration through the
  [environment abstraction](../../framework/agent/environment/). Honor explicit
  discovery scopes. Missing requirements make that action unavailable without
  disabling unrelated actions; report actionable requirement metadata.
- Advertise secret names, never values. Recheck required configuration during
  execution and invalidate stale discovery state when rediscovering dependencies.
- Declare required permissions on actions and grant permissions explicitly in host
  configuration. Constructors must not silently grant their own requirements.
- Catalog entries describe capabilities; per-agent readiness, grants and leases
  determine availability. Do not claim that installing one agent guarantees
  complete required-action coverage for a mission.

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

Run the affected agent module from the repository root, for example:

```sh
go test -race -count=1 -timeout=3m ./apps/agents/web-quality/...
go vet ./apps/agents/web-quality/...
```

[Unit CI](../../.github/workflows/test-unit.yml) covers the bundled host. Test
discovery failures, permission boundaries and local fixtures without paid models
or public websites. Use [adapter scenarios](../../framework/tests/README.md#mcp-api-tests)
for behavior spanning agents, controller persistence and mission automation.

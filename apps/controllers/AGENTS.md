# Working on controller hosts

## Host wiring and lifecycle

- [default/cli/cli.go](default/cli/cli.go) composes registration, coordination,
  the runner and adapter-command consumer. Configuration lives in `cli/config.go`;
  concrete providers and their cleanup belong in `cli/factory.go`.
- Keep framework scheduling and recovery in [framework/controller](../../framework/controller/).
  A host supplies dependencies and policies; it must not duplicate the runner's
  owner loop or introduce its own operation mutation path.
- Mark registration ready only after the runner and consumers start. On shutdown,
  mark draining, stop command intake, close the runner, then coordination, then
  registration and providers. Keep coordination available while the runner suspends
  accepted tasks, and use bounded shutdown contexts that survive parent cancellation.
- Keep the current one-active-controller-per-database assumption explicit.
  Registration and provider support do not implement distributed ownership or
  capacity across controllers.

## Selection and durable work

- [runner.AgentSelector](../../framework/controller/runner/agent_selector.go)
  chooses only among supplied eligible candidates. Readiness, permissions, capacity
  checks and durable dispatch reservation remain the runner's responsibility.
- Selection runs serially in the owner loop. Return promptly, honor cancellation,
  and do not dispatch work or retain mutable candidate snapshots. An empty ID
  defers work; errors or invalid IDs leave it queued.
- Report the selector's `Name()` in registration. The previous selected agent ID
  advances only after durable dispatch reservation; load/capacity describe this
  runner's work, not a distributed global limit.
- Dispatched tasks remain bound to their recorded agent and command. Never silently
  reassign an execution whose outcome is unknown or retry it from host code.
- Lifecycle changes need coordinated persistence, consumers and restart recovery.
  Reconcile uncertain writes before dispatching more work; preserve immutable
  execution outcomes, accepted artifact ownership and reports.

## Mission loading

- [default/data/missions.go](default/data/missions.go) validates all file definitions
  before upserting any. Preserve missions omitted from the file; no `--with-missions`
  means no import. An operation retains the mission definition saved at its start.
- Update [default-missions.json](default/data/default-missions.json) for mission
  automation. Agents propose actions, while the controller applies these rules;
  do not move mission policy into host or agent special cases.

## Validation

From the repository root:

```sh
go test -race -count=1 -timeout=3m ./apps/controllers/default/...
go vet ./apps/controllers/default/...
```

- [Host tests](default/tests/) cover registration, startup, imports and the bundled
  mission. Mongo startup coverage requires `COMMANDER_TEST_MONGO_URL` and a replica
  set; report that coverage as skipped if the variable is absent.
- [Integration CI](../../.github/workflows/test-integration.yml) supplies MongoDB;
  [unit CI](../../.github/workflows/test-unit.yml) covers the host without it.
  Follow [Mongo setup](../../providers/mongo/README.md#development-and-testing) for
  service-backed checks, and stop only the test services/state you started.
- Scheduler or recovery changes need affected framework tests plus public
  [MCP/API scenarios](../../framework/tests/README.md#mcp-api-tests), including
  restart, uncertain writes and settled results where relevant.

# Working on controller hosts

## Host wiring and lifecycle

- [default/cli/cli.go](default/cli/cli.go) composes registration and the controller.
  Configuration lives in `cli/config.go`;
  concrete providers and their cleanup belong in `cli/factory.go`.
- Keep framework scheduling and recovery in [framework/controller](../../framework/controller/).
  A host supplies dependencies and policies; it must not duplicate the operation
  worker's owner loop or introduce its own operation mutation path.
- Supply mission, operation and report stores through `ControllerConfig`.
  The controller constructs, recovers, starts and joins its operation worker.
  Assembly supplies its operation commands directly to the owned command worker.
  Do not create another operation worker or command consumer, or manage worker
  `Run` calls from the CLI.
- Mark registration ready only after controller construction completes recovery
  and subscribes command/cancellation inboxes. On shutdown, mark draining, close
  the controller, then registration and providers. Controller shutdown stops
  admission and joins command requests and subscription cleanup, then the
  operation worker, before closing coordination and events. Use bounded shutdown contexts that
  survive parent cancellation. Do not add an unbounded worker wait after an
  expired `Close` deadline.
- Keep the current one-active-controller-per-database assumption explicit.
  Registration and provider support do not implement distributed ownership or
  capacity across controllers.
- Construct both secret and request stores from the configured database and
  inject them into coordination. `Options.SecretCipher` overrides optional
  `SECRETS_ENCRYPTION_KEY`; validate the supplied cipher/key before opening providers.
  Missing keys prevent secret-value delivery; never generate a replacement key.
  Inject the decryptor into coordination, which reserves encrypted evidence then
  publishes only the requested value and metadata. Framework reconciliation owns
  delivery retries, pending request recovery and installation acknowledgements.
- `ExecutionStore` owns claim, checkpoint, result and log persistence through the
  existing filesystem/Mongo selection. Keep log records in separate collections;
  the same wiring serves the embedded Playground controller. Log events verify their
  saved execution claim and persist before delivery ACK. Log-processing failures
  reject the event without ending coordination; transport/settlement failures
  remain fatal. Logging never changes task deadlines or outcomes.

## Selection and durable work

- [operation.AgentSelector](../../framework/controller/workers/operation/agent_selector.go)
  chooses only among supplied eligible candidates. Session ownership, readiness, capacity
  checks and durable dispatch reservation remain the operation worker's
  responsibility.
  Inject it through `ControllerConfig.AgentSelector`.
- Selection runs serially in the owner loop. Return promptly, honor cancellation,
  and do not dispatch work or retain mutable candidate snapshots. An empty ID
  defers work; errors or invalid IDs leave it queued.
- Report the selector's `Name()` in registration. The previous selected agent ID
  advances only after durable dispatch reservation; load/capacity describe this
  operation worker's work, not a distributed global limit.
- The operation worker validates the stored mission and retains its policies
  directly in the operation's independent mission snapshot. Hosts do not inject policy selection
  or override policy. Edit mission definitions for future operations; recovery
  uses each operation's original snapshot.
- Dispatched tasks remain bound to their recorded agent and command. Never silently
  reassign an execution whose outcome is unknown or retry it from host code.
- `--task-timeout` overrides `TASK_TIMEOUT`; both require a positive Go duration
  and default to `operations.DefaultTaskTimeout` (15 minutes). Pass the value through
  `ControllerConfig.TaskTimeout`, including embedded hosts. This is the initial
  result wait for new dispatches;
  retain saved deadlines during recovery. Heartbeats, questions and log activity
  do not extend the deadline. Expiry does not cancel the remote process.
- Lifecycle changes need coordinated persistence, consumers and restart recovery.
  Reconcile uncertain writes before dispatching more work; preserve immutable
  execution outcomes, accepted artifact ownership and reports.

## Mission definitions

- Admin owns bundled missions in
  [default-missions.json](../admin/missions/default-missions.json) and seeds them
  through the API. The controller reads mission definitions from its store; it
  must not seed or import them during startup.
- Mission behavior belongs in its policies. Agents propose
  actions; the controller applies the saved definition without host or agent
  special cases.
- Existing operations retain their mission snapshot when the catalog changes.
  Keep mission validation and authoring behavior in the framework and Admin.

## Validation

From the repository root:

```sh
go build ./apps/controllers/default/...
go vet ./apps/controllers/default/...
```

- [Host integration](default/tests/) covers startup with MongoDB. It requires `COMMANDER_TEST_MONGO_URL` and a replica set;
  report that coverage as skipped if the variable is absent.
- [Integration CI](../../.github/workflows/test-integration.yml) supplies MongoDB;
  [build/static CI](../../.github/workflows/test-unit.yml) covers the host without it.
  Follow [Mongo setup](../../providers/mongo/README.md#development-and-testing) for
  service-backed checks, and stop only the test services/state you started.
- Scheduler or recovery changes need build/static checks plus public
  [MCP/API scenarios](../../framework/tests/README.md#mcp-api-tests), including
  restart, uncertain writes and settled results where relevant.

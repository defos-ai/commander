# Working on controller workers

[NewController](../controller.go) constructs admission and core lifetimes, then
owns worker startup and joining; each worker owns its cleanup.
[Assembly](../assembly.go) receives the existing host and `ControllerConfig`, builds
the list of [Worker](worker.go) instances and returns it without calling `Run` or
starting workers.
Workers never import the parent controller package or construct providers.
Dependency structs describe the stores and capabilities each worker consumes.
This package owns the common worker contract and shared [Lifecycle](lifecycle.go)
helper; implementation subpackages expose `Worker`, `Dependencies` and `New`.
If assembly fails, cancel both lifetimes and close every constructed worker,
including workers whose `Run` has not started. No runtime loop starts in assembly.

- [Commands](command/worker.go) owns adapter command and cancellation subscriptions,
  concurrent request handlers and their private request ledger. Subscribe after
  runner recovery and before readiness. Keep request ID/adapter/payload checks,
  cached replies and cancellation notices that overtake requests intact. The ledger
  owner must keep processing completions until both consumers join their handlers.
  Cancel before independently closing both subscriptions; `Close` joins Run and
  once-only subscription cleanup within the caller's deadline. Intake failures
  stop admission through the ordered shutdown path; they do not cancel core
  routing before the runner joins.
- [Coordination](coordination/worker.go) owns the command mailbox, pending replies,
  inventory observation state, lease expiry and durable dispatch/recovery. Remote
  waits stay outside its owner loop. `submit` retains a buffered reply so caller
  cancellation does not block the owner or discard value-plus-error outcomes.
- [Events](event/worker.go) owns the subscription and handlers. Apply observations
  inside coordination before acknowledging a delivery. Fatal observation errors
  fence admission before releasing the mailbox; rejected messages and log write
  failures retain their existing recoverable behavior. Sharing follows acceptance.
  `Close` cancels first and starts subscription cleanup before joining `Run`.
  Run subscription cleanup once, independently of the caller's deadline; retries
  await the same cleanup and preserve its error.
- [Sessions](session/worker.go) owns one gate, admission caches, preparation,
  publication and retrying retained controls. Share this exact instance with the
  runner, events and dispatch. Keep remote control waits outside the session gate.
  Follow the [session protocol](../../sessions/AGENTS.md) for changes.
- [Secrets](secret/worker.go) owns encrypted delivery handlers and its retry loop.
  Both event handling and reconciliation execute inside coordination; never
  recursively submit from an owner callback. Startup reconciliation runs before
  workers start. Fatal writes fence new commands before returning.
- [Sharing](sharing/worker.go) owns its bounded queue and best-effort forwarding.
  Read accepted records and observe recipients through `AgentReader`; never
  mutate coordination state or change accepted execution outcomes on failure.
- [Runner](runner/worker.go) owns operation scheduling and recovery. Controller
  assembly supplies mandatory mission, operation and report stores, the same
  session worker, an explicit sharing capability and a typed coordination
  executor. Keep its catalog and execution paths independent of public host
  admission so accepted results can drain during shutdown. Assembly injects it
  into the command worker; hosts do not construct a second runner or command consumer.

`coordination.Worker.Apply` scopes event and secret mutation to its owner loop.
Keep the supplied state inside that synchronous callback; events bind a local
consumer copy rather than retaining the state pointer. The command path keeps
its private typed submission helper to preserve value-plus-error replies.

The common contract includes blocking `Run(ctx)` and `Close(ctx)`.
The shared [lifecycle helper](lifecycle.go) owns single-use
Run admission, cancellation and joining for core workers. `Close` before `Run`
stops the worker permanently; concurrent Run/Close calls must not close completion
twice. Run errors retain the controller's fatal cause; cleanup errors return
through `Close`.

Keep the shutdown hook limited to lifecycle shutdown. Do not pass Controller or
a collection of host callbacks into workers. Cancel before releasing pending
callers so they see the original fatal cause. Retain the complete worker list
before starting any Run call. Shared admission cancellation can begin command and
runner draining together. Graceful shutdown joins command requests and cleanup,
then the runner, before canceling and closing core workers concurrently outside
owner callbacks. Coordination and events remain available during both drains.
Fatal core errors cancel core work immediately and fence pending callers
with their original cause. The caller's deadline bounds waiting, and retries await
the same cleanup.

Run isolated framework build, vet and race tests, downstream controller/API host
checks and the session E2Es after ownership or lifecycle changes. Extend retained
[lifecycle scenarios](../../tests/controller/lifecycle_test.go) for fatal admission
and bounded shutdown; avoid constructor-only worker tests.

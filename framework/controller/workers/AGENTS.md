# Working on controller workers

[NewController](../controller.go) constructs the host and shared lifetime, then
owns worker startup and joining; each worker owns its cleanup.
[Assembly](../assembly.go) receives the existing host and `ControllerConfig`, builds
the list of [Worker](worker.go) instances and returns it without calling `Run` or
starting workers.
Workers never import the parent controller package or construct providers.
Dependency structs describe the stores and capabilities each worker consumes.
This package owns the common worker contract and shared [Lifecycle](lifecycle.go)
helper; implementation subpackages expose `Worker`, `Dependencies` and `New`.
If assembly fails, cancel the shared lifetime before closing every constructed
worker, including workers whose `Run` has not started.

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
- [Runner](runner/worker.go) owns operation scheduling and recovery. It now lives
  here but remains outside the controller's assembled worker list: hosts still
  start and join its single-use `Run` explicitly. Preserve the existing shutdown
  order until controller ownership is introduced separately.

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
before starting any Run call. Host shutdown cancels first, closes workers
concurrently outside owner callbacks and joins them within its caller's deadline.
Retries await the same cleanup and preserve its error.

Run isolated framework build, vet and race tests, downstream controller/API host
checks and the session E2Es after ownership or lifecycle changes. Extend retained
[lifecycle scenarios](../../tests/controller/lifecycle_test.go) for fatal admission
and bounded shutdown; avoid constructor-only worker tests.

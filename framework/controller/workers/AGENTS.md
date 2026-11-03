# Working on controller workers

[NewController](../controller.go) constructs the host and shared lifetime, then
owns worker startup, joining and subscription cleanup.
[Assembly](../assembly.go) receives the existing host and `ControllerConfig`, builds
the list of [Worker](worker.go) instances and returns it without calling `Run` or
starting workers.
Workers never import the parent controller package or construct providers.
Dependency structs describe the stores and capabilities each worker consumes.
This package contains only the common worker contract; implementation subpackages
expose `Worker`, `Dependencies` and `New`.
If assembly fails, cancel the shared lifetime before closing its Events
subscription.

- [Coordination](coordination/worker.go) owns the command mailbox, pending replies,
  inventory observation state, lease expiry and durable dispatch/recovery. Remote
  waits stay outside its owner loop. `submit` retains a buffered reply so caller
  cancellation does not block the owner or discard value-plus-error outcomes.
- [Events](event/worker.go) owns the subscription and handlers. Apply observations
  inside coordination before acknowledging a delivery. Fatal observation errors
  fence admission before releasing the mailbox; rejected messages and log write
  failures retain their existing recoverable behavior. Sharing follows acceptance.
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

`coordination.Worker.Apply` scopes event and secret mutation to its owner loop.
Keep the supplied state inside that synchronous callback; events bind a local
consumer copy rather than retaining the state pointer. The command path keeps
its private typed submission helper to preserve value-plus-error replies.

Keep the shutdown hook limited to lifecycle cancellation. Do not pass Controller
or a collection of host callbacks into workers. Cancel before releasing pending
callers so they see the original fatal cause. Host `Close` joins workers and
independent subscription cleanup within its caller's deadline; retries must keep
waiting for the same cleanup and preserve its error.

Run isolated framework build, vet and race tests, downstream controller/API host
checks and the session E2Es after ownership or lifecycle changes. Extend retained
[lifecycle scenarios](../../tests/controller/lifecycle_test.go) for fatal admission
and bounded shutdown; avoid constructor-only worker tests.

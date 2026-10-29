# Working on messaging

## Contract ownership

- [`bus.go`](bus.go) and [`inbox.go`](inbox.go) define provider-independent delivery
  and inbox contracts; [`consumer.go`](consumer.go) owns shared consumption.
- Keep typed commands, events and publishing clients in [`clients`](clients/),
  grouped by recipient. Envelope and routing changes have additional guidance in
  [`messages/AGENTS.md`](messages/AGENTS.md).
- A delivery owns its settlement independently of every other copy with the same
  message ID. Repeating a successful settlement is harmless; the opposite outcome
  returns `ErrDeliverySettled`. A failed settlement may be retried.
- Inbox identity is `(Topic, Name)` with exact topic matching. Providers must
  reject unsupported modes with `ErrUnsupportedInbox`, never downgrade them.
- A subscription's creation context only controls creation. Canceling one `Next`
  must not invalidate earlier deliveries. Closing a subscription must not ACK
  outstanding work or delete a durable inbox.

## Consumer behavior

- Hosts own cancellation and subscription lifetime; only one `Consume` call may
  read a subscription. The consumer awaits active handlers before returning.
- `nil` from a handler means required processing finished and permits ACK. A
  recorded business failure may return nil; incomplete processing returns error.
- Handler errors reject that delivery. `ContinueOnError` permits continued
  consumption after rejection, without retrying or requeueing the handler.
  Receive, settlement and cancellation errors always stop consumption.
- Keep required processing before ACK; `AfterAck` is only local bookkeeping.
  Default consumption is sequential. Higher concurrency requires safe handlers;
  callbacks and settlement remain serialized and deliveries remain independent.
- Business idempotency belongs to handlers. Do not add automatic mutation retries
  or imply exactly-once execution. Broker ACK is separate from durable controller
  acceptance of an agent result.

## Replay and time

- Preserve the adapter client's subscribe-before-publish behavior. A lost reply
  does not authorize retrying a mutation. Controller request replay is bounded
  and in memory; it does not survive controller restart.
- Execution checkpoint requests are explicitly retryable: reads and identical
  immutable saves reuse their request identity while the action is active.
  Controller replies follow saved-claim validation and durable persistence.
  This does not extend adapter mutation replay or authorize rerunning an action.
- Agent command expiry controls admission of new work, not cancellation of
  running work or replay of cached outcomes. Recovery preserves the deadline.
- Lifecycle observations use source timestamps, including lease renewal. Late
  action results may persist without rolling back newer inventory. Keep the
  consumers' stale/future observation checks when changing envelope handling.

## Validation

For consumer behavior, run from the repository root:

```sh
go build ./framework/comms/...
go vet ./framework/comms/...
```

Run the parent framework checks for client or contract changes. Bus/inbox changes
also require affected FS and RabbitMQ suites; use their local AGENTS files and the
[testing guide](../tests/README.md) for services and setup.

# Messaging

`Bus` transports messages; the controller chooses the agent that receives work.
Commands and events connect Commander through its adapter to the controller,
then the controller to agents. See [wire contracts](messages/README.md) for routes.

## Consuming messages

Hosts open subscriptions and manage their lifetime. Domain consumers implement
`Consume(ctx, message) error`; the shared `Consumer.Consume` receives and settles
deliveries returned by `Subscription.Next`:

```go
consumer := comms.Consumer{Subscription: subscription, Logger: logger}
err := consumer.Consume(ctx, domainConsumer.Consume)
```

| Handler outcome | Consumer behavior |
| --- | --- |
| `nil` | Call `Ack(ctx)` on that delivery after processing. |
| Error | Call `Reject(ctx)` on that delivery, then stop unless `ContinueOnError` permits continuing. |
| Receive, ACK, rejection error or cancellation | Stop consumption. |

A business failure can be handled successfully: persist or publish its failure
outcome, then return nil. Returning an error means processing itself is incomplete.
`AfterAck` runs local bookkeeping only after acknowledgement succeeds.
`ContinueOnError` runs only after rejection succeeds.

The consumer does not rerun handlers, deduplicate business work or schedule retries.
`ContinueOnError` does not requeue the failed message. Retention and redelivery
belong to the provider.

## Domain consumers

| Location | Responsibility |
| --- | --- |
| [Agent consumers](../agent/consumers) | Run actions, discover readiness, stop gracefully, accept result receipts. |
| [Controller event consumers](../controller/consumers) | Persist agent observations and command outcomes. |
| [Controller adapter consumer](../controller/consumers/adapter) | Execute operation commands and respond to the adapter. |

Commands, results, receipts and cancellation use separate subscriptions where
needed, so waiting for a long action does not block receipt of its result.

Processing is sequential by default. `Concurrency > 1` bounds simultaneous
handlers and requires concurrency-safe handlers; callbacks stay serialized.
Each `Delivery` exposes `Message()` and owns its `Ack(ctx)` / `Reject(ctx)` outcome.
Copies with the same message ID settle independently. Repeating the same successful
settlement on a handle is harmless; choosing the opposite outcome returns
`ErrDeliverySettled`. Settlement state belongs to the delivery handle.

## Delivery guarantees

`Subscribe(ctx, comms.Inbox{Topic: topic, Name: name, Mode: mode})` opens an inbox.
Identity is `(Topic, Name)`; different names on the same exact topic receive
independent copies. All production hosts use their process ID as the name and
`TransientExclusive` mode.

| Mode | Contract |
| --- | --- |
| `TransientExclusive` | One active consumer; closing or losing it ends the inbox. Reopening receives new publications only. |
| `DurableShared` | Retains work from first declaration through downtime; consumers sharing the inbox compete. Unsettled work can be redelivered and processing order is not guaranteed. |

Only `TransientExclusive` is implemented by FS. `DurableShared` returns
`ErrUnsupportedInbox`. The [RabbitMQ provider](../../providers/rabbitmq/README.md)
implements both modes. Hosts currently select `TransientExclusive` with either
provider; shared durable controller inboxes are not activated.
FS retains unsettled and rejected evidence without replaying it into a new
subscription; see [FS behavior](../../providers/fs/README.md).

Broker acknowledgement and business acceptance are different. The controller
sends an agent a result receipt only after durable persistence. The agent retries
unconfirmed outcomes from its process cache. See the
[result protocol](messages/README.md#correlation-and-acknowledgements).

## Adapter command replay

The controller retains up to 4,096 running or completed adapter requests in
memory. Duplicate requests share the original execution and reply. Completed
replies remain protected until their original deadlines expire; running handlers
stay tracked until completion even if their contexts have been canceled.

When the ledger is full, new requests wait for space, bounded by their own
deadlines and consumer shutdown. An expired request with no retained reply gets
a timeout response without executing. Only completed, expired entries can be
removed to admit more work. Cancellation messages have a separate consumer.

This protects redelivery within the same controller process. Durable deduplication
is still required before enabling shared command queues across controller restarts.

## Command admission deadlines

The controller sets `expires_at` on agent commands from the caller/task deadline,
with a five-minute start window when no deadline is supplied. An agent reports a
failure if new action, discovery, or stop work arrives too late. Expiry does not
cancel work already started.

Action deduplication runs before this check: a completed action replays its saved
result even after expiry. Recovery preserves the original command and deadline;
retrying cannot extend them. Hand-built commands with no expiry have no admission
limit. Host clocks must be synchronized.

## Delayed agent events

Agent inventory applies source observations in timestamp order. Replays cannot
roll back readiness or current work, and heartbeats renew leases from their
source time, not arrival time. Lifecycle events older than the heartbeat timeout
are ignored. Agent and controller clocks must stay synchronized; timestamps more
than five seconds ahead of the controller are rejected.

Stores briefly retain a hidden ordering marker after lease expiry so delayed
snapshots cannot recreate older state. A fresh heartbeat can restore the agent.
Late action results still persist against their execution record and receive
acceptance, without changing newer inventory.

## RabbitMQ provider

The [RabbitMQ provider](../../providers/rabbitmq/README.md) implements the bus
contract, including both inbox modes and publish confirmations. Host factories
select it for `amqp://` and `amqps://` bus URLs. Full Compose uses RabbitMQ;
the default Compose stack uses FS. Hosts use transient inboxes in both stacks.
Controller-owned assignment and result acceptance remain framework responsibilities.
Connecting a broker alone does not make multiple controllers safe;
see [current scope](../../docs/overview.md#current-scope).

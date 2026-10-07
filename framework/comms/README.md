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

A handled business failure can return nil after saving or publishing its outcome.
Returning an error means processing is incomplete. The consumer does not retry
handlers or deduplicate work; `ContinueOnError` does not requeue a failed message.
Processing is sequential unless `Concurrency` is set above one.
See [`Consumer`](consumer.go) for callback and concurrency options, and
[`Delivery`](bus.go) for settlement semantics.

## Delivery guarantees

`Subscribe(ctx, comms.Inbox{Topic: topic, Name: name, Mode: mode})` opens an inbox.
Identity is `(Topic, Name)`; different names on the same exact topic receive
independent copies. Hosts use their process ID as the name and `TransientExclusive` mode.

| Mode | Contract |
| --- | --- |
| `TransientExclusive` | One active consumer; closing or losing it ends the inbox. Reopening receives new publications only. |
| `DurableShared` | Retains work from first declaration through downtime; consumers sharing the inbox compete. Unsettled work can be redelivered and processing order is not guaranteed. |

The [FS provider](../../providers/fs/README.md#delivery-and-recovery) supports only
transient inboxes and retains rejected files without replaying them. The
[RabbitMQ provider](../../providers/rabbitmq/README.md#delivery-and-recovery)
supports both modes. Shared durable controller inboxes are not activated;
one active controller per database is supported.

Broker acknowledgement and business acceptance are different. The controller
sends an agent a result receipt only after durable persistence. The agent retries
unconfirmed outcomes from its process cache. See the
[result protocol](messages/README.md#correlation-and-acknowledgements).

## Adapter command replay

Duplicate adapter requests share their execution and reply while retained by the
controller's bounded in-memory ledger. New requests may wait for space until
their deadline. An expired request without a retained reply times out without
executing. Replay protection does not survive controller restarts; see the
[`adapter` consumer](../controller/consumers/adapter/).

## Command admission deadlines

The controller sets `expires_at` on agent commands from the caller/task deadline,
with a five-minute start window when no deadline is supplied. An agent reports a
failure if new action, discovery, or stop work arrives too late. Expiry does not
cancel work already started.

Completed actions can replay cached results after expiry. Recovery preserves the
original deadline. Hand-built commands with no expiry have no admission limit.
Host clocks must be synchronized.

## Delayed agent events

Agent inventory applies source observations in timestamp order. Replays cannot
roll back readiness or current work, and heartbeats renew leases from their
source time, not arrival time. Lifecycle events older than the heartbeat timeout
are ignored. Agent and controller clocks must stay synchronized; timestamps more
than five seconds ahead of the controller are rejected.

Late action results still persist against their execution record and receive
acceptance, without changing newer inventory. See
[restart recovery](../../docs/overview.md#state-and-restart-recovery) for limits.

For changes to this package, see [coding guidance](AGENTS.md).

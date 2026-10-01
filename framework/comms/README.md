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

The current `Bus` contract fans out to **current subscribers of an exact topic**.
It does not define durable queues or competing consumer groups. The FS provider
retains unsettled and rejected deliveries but does not replay them into a new subscription;
see [FS behavior](../../providers/fs/README.md).

Broker acknowledgement and business acceptance are different. The controller
sends an agent a result receipt only after durable persistence. The agent retries
unconfirmed outcomes from its process cache. See the
[result protocol](messages/README.md#correlation-and-acknowledgements).

## Future broker providers

RabbitMQ is not implemented. Adding it needs explicit subscription lifetime,
competing-consumer behavior, retry/failed-delivery policy and publish confirmation
semantics. Those changes must preserve controller-owned assignment and result
acceptance. Connecting a broker alone does not make multiple controllers safe;
see [current scope](../../docs/overview.md#current-scope).

# Messaging

Connect adapters, the controller and agents through a shared messaging contract.
Commands and events carry work between services; inbox modes and explicit
delivery settlement give hosts control over failures and duplicate deliveries.
Use `comms.Bus` with the
[FS or RabbitMQ provider](../../providers/README.md) in your host factory.

## Consuming messages

Open a subscription before publishing, then let `Consumer` handle delivery settlement:

```go
consumer := comms.Consumer{Subscription: subscription, Logger: logger}
err := consumer.Consume(ctx, domainConsumer.Consume)
```

Return `nil` after completing processing; return an error to reject the delivery.
Processing stops on errors unless `ContinueOnError` permits the next message.
Use `Concurrency` only when your handler is safe to run concurrently. Close the
subscription after consumption stops, then close the bus.

## Delivery guarantees

Choose an inbox mode when subscribing:

| Mode | Use when… | Providers |
| --- | --- | --- |
| `TransientExclusive` | Only publications received while this host is connected matter. | FS, RabbitMQ |
| `DurableShared` | Work must wait through downtime; consumers can share an inbox. | RabbitMQ |

Different inbox names on the same topic receive independent copies. Commander
hosts use transient inboxes. Run only one controller per database.

Handle duplicates in your application. A broker acknowledgement does not confirm
that the controller saved an action result; see
[result acceptance](messages/README.md#correlation-and-acknowledgements).

## Adapter command replay

After a lost mutation reply, inspect saved state before retrying. Replay protection
is bounded and does not survive a controller restart.

## Command admission deadlines

Set deadlines and synchronize host clocks. `expires_at` limits when new work can
start; it does not cancel work already running.

## Delayed agent events

Use saved operation results to investigate late outcomes. Delayed inventory events
cannot restore an older readiness snapshot. See
[restart recovery](../../docs/overview.md#state-and-restart-recovery) and
[implementation guidance](AGENTS.md).

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.

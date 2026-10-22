# RabbitMQ provider

Connect Commander hosts through AMQP 0-9-1. Set `BUS_URL` to an `amqp://` or
`amqps://` URL. [Compose](../../compose.yaml) configures the broker automatically.

## Quick start

For an external broker, provide its connection URL to every host. In Compose,
`RABBITMQ_USER` and `RABBITMQ_PASSWORD` set initial broker credentials.
See [deployment](../../docs/deployment.md#local-compose) to start the stack.

## Public API

Create one bus per host:

```go
import (
    "github.com/defos-ai/commander/framework/comms"
    rabbitcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
)

bus, err := rabbitcomms.NewRabbitMQBus(ctx, brokerURL,
    rabbitcomms.Options{Prefetch: 16})
if err != nil {
    return err
}
defer bus.Close()

subscription, err := bus.Subscribe(ctx, comms.Inbox{
    Topic: "example.events",
    Name:  "example-worker",
    Mode:  comms.DurableShared,
})
if err != nil {
    return err
}
defer subscription.Close()
```

Consume with [`comms.Consumer`](../../framework/comms/README.md#consuming-messages).
Stop consumers before closing subscriptions and the bus.

## Delivery and recovery

Choose `TransientExclusive` for a live host inbox or `DurableShared` to retain
messages through consumer downtime. Declare subscriptions before publishing.
Commander hosts use transient inboxes; run only one controller per database.

Inspect rejected or malformed messages in `commander.failed`. Recreate the bus
and subscriptions after connection loss; reconnect and retries are not automatic.
Handle uncertain publish outcomes without assuming exactly-once delivery.

## Development and testing

Use a dedicated broker: tests change failed-queue bindings. From the repository root:

```sh
docker compose -f .github/composes/compose.providers.test.yaml up --wait rabbitmq
COMMANDER_TEST_RABBITMQ_URL='amqp://commander-test:commander-test-secret@127.0.0.1:5673/' \
  go test -race -count=1 -timeout=3m ./providers/rabbitmq/...
go vet ./providers/rabbitmq/...
docker compose -f .github/composes/compose.providers.test.yaml down --volumes
```

Tests skip without `COMMANDER_TEST_RABBITMQ_URL`. Stop the stack after failures too.
See [AGENTS.md](AGENTS.md) for implementation guidance.

# RabbitMQ provider

## Overview

`github.com/defos-ai/commander/providers/rabbitmq` implements `comms.Bus` over
AMQP 0-9-1 with [`amqp091-go`](https://github.com/rabbitmq/amqp091-go). It supports
`amqp://` and `amqps://` URLs and both framework inbox modes. Controller, MCP, and
browser agent factories select
this provider when `BUS_URL` or `--bus-url` uses either scheme.
[`compose.yaml`](../../compose.yaml) selects RabbitMQ; all-in-one uses FS.

Hosts use transient inboxes, so a replacement host does not inherit its old
queue. One active controller per database is supported.

## Quick start

From the repository root:

```sh
docker compose up --build -d
```

The full stack supplies `amqp://commander:commander-local-secret@rabbitmq:5672/`
to its hosts. Broker and management ports are not published to the host.
`RABBITMQ_USER` and `RABBITMQ_PASSWORD` set initial broker credentials;
`BUS_URL` can select an external broker.

See [local setup](../../docs/deployment.md#local-compose) for MCP and Playground.

## Public API

Import `github.com/defos-ai/commander/providers/rabbitmq/comms` and construct one
bus per host:

```go
import (
    "github.com/defos-ai/commander/framework/comms"
    rabbitcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
)

// ctx bounds connection and subscription creation.
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

// Publish with bus.Publish(ctx, topic, message).
// Consume with comms.Consumer, or Next followed by Delivery.Ack / Reject.
```

`Options.Prefetch` bounds unacknowledged deliveries per subscription; zero uses
16. Close subscriptions after consumers stop, then close the bus. See the
[consumer contract](../../framework/comms/README.md) for receiving and settlement.

The workspace includes this module. Private projects use
[local module replacements](../../apps/agents/README.md#develop-an-agent-in-a-private-repository)
for the framework and this provider.

## Delivery and recovery

The provider declares the durable direct exchange `commander`.
Topics match exactly. Inbox identity is `(Topic, Name)`; different names on the
same topic receive independent copies. [`QueueName(inbox)`](comms/topology.go)
returns the stable broker queue name.

| Inbox mode | RabbitMQ behavior |
| --- | --- |
| `TransientExclusive` | Exclusive, auto-delete queue with one consumer. Closing or losing the subscription ends the inbox; reopenings receive new publications only. |
| `DurableShared` | Durable classic queue retained through consumer downtime. Consumers sharing an inbox compete; unsettled deliveries can be redelivered. Closing a subscription leaves the inbox intact. |

Declare inboxes before publishing. Publications are persistent and mandatory;
`Publish` waits for a broker confirmation and returns `ErrUnroutable` when no
inbox accepts the route. A publish error can leave the outcome uncertain, so
application retries must tolerate duplicate delivery. Broker confirmation does
not mean a consumer has processed or accepted the message.

Rejected and malformed messages go to the durable `commander.failed` queue for
inspection, with source topic and inbox headers. There is no automatic retry loop.
Connection loss can leave settlement uncertain and create duplicate failure records.
There is no automatic reconnect: the host must recreate the bus and subscriptions.
Setup, publication and settlement have a 30-second upper bound, shortened by caller
deadlines; a publication timeout closes the publisher connection. Delivery is not
exactly once. See [delivery handling](comms/delivery.go) for details.

Agents enforce `expires_at`; it is not a broker TTL. Durable queues do not add
business deduplication across controller restarts or multi-controller support.

## Development and testing

See [AGENTS.md](AGENTS.md) for topology and delivery-settlement guidance.

Start a dedicated test broker from the repository root:

```sh
docker compose -f .github/composes/compose.providers.test.yaml up --wait rabbitmq
COMMANDER_TEST_RABBITMQ_URL='amqp://commander-test:commander-test-secret@127.0.0.1:5673/' \
  go test -race -count=1 -timeout=3m ./providers/rabbitmq/...
go vet ./providers/rabbitmq/...
docker compose -f .github/composes/compose.providers.test.yaml down --volumes
```

Integration tests skip without `COMMANDER_TEST_RABBITMQ_URL`. Use a dedicated
broker or virtual host: tests modify the shared failed-delivery queue's bindings.
The test broker exposes localhost port `5673`. Always stop it after failures too.

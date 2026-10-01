# RabbitMQ provider

## Overview

`commander/providers/rabbitmq` implements `comms.Bus` over AMQP 0-9-1 with
[`amqp091-go`](https://github.com/rabbitmq/amqp091-go). It supports `amqp://` and `amqps://` URLs and both
framework inbox modes. This module can be used directly; production host
factories and both Compose stacks still use the FS bus. Setting a host's
`BUS_URL` to RabbitMQ still returns a not-implemented error.

## Public API

Import `commander/providers/rabbitmq/comms` and construct one bus per host:

```go
import (
    "commander/framework/comms"
    rabbitcomms "commander/providers/rabbitmq/comms"
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
16. Publication uses a separate connection; each subscription owns its consuming
connection. Close subscriptions after consumers stop, then close the bus. Canceling a
`Next` call ends that receive without closing the subscription or invalidating
previously returned deliveries. See the [consumer contract](../../framework/comms/README.md).

The workspace includes this module. Private projects use
[local module replacements](../../apps/agents/README.md#develop-an-agent-in-a-private-repository)
for `commander/framework` and `commander/providers/rabbitmq`.

## Delivery and recovery

The provider declares the durable direct exchange `commander` (`Exchange`).
Topics match exactly. Inbox identity is `(Topic, Name)`; different names on the
same topic receive independent copies. `QueueName(inbox)` returns the stable
broker queue name, containing a readable topic prefix and an identity hash.

| Inbox mode | RabbitMQ behavior |
| --- | --- |
| `TransientExclusive` | Exclusive, auto-delete queue with one consumer. Closing or losing the subscription ends the inbox; reopenings receive new publications only. |
| `DurableShared` | Durable classic queue retained through consumer downtime. Consumers sharing an inbox compete; unsettled deliveries can be redelivered. Closing a subscription leaves the inbox intact. |

Declare inboxes before publishing. Publications are persistent and mandatory;
`Publish` waits for a broker confirmation and returns `ErrUnroutable` when no
inbox accepts the route. A publish error can leave the outcome uncertain, so
application retries must tolerate duplicate delivery. Broker confirmation does
not mean a consumer has processed or accepted the message.

`Ack` acknowledges processing. `Reject` publishes the original raw body to the
durable fanout exchange `commander.failed` (`FailedExchange`) and its durable
queue `commander.failed` (`FailedQueue`), with source topic and inbox headers.
Only after that publication is confirmed does it acknowledge the original.
Failed deliveries remain available for inspection; there is no automatic retry
loop. A malformed incoming message follows the same quarantine path, and
`Next` returns `comms.ErrInvalidMessage` after successful quarantine.

Repeating the same successful settlement on a delivery is harmless; selecting
the opposite outcome returns `comms.ErrDeliverySettled`. Connection loss can
make settlement uncertain and can produce duplicate failed-delivery records.
There is no automatic reconnect: connection failures surface to callers, and
the host must recreate the bus and subscriptions. Setup, publication and settlement
use a 30-second upper bound, shortened by caller deadlines. A publication timeout
closes the publisher connection because its outcome is uncertain. Delivery is
not exactly once.

The provider does not convert `expires_at` into a broker TTL; agents enforce
command admission deadlines. Controller assignment and result acceptance stay
in the framework. Durable queues alone do not make multiple controllers safe:
durable business deduplication across controller restarts remains future work.

## Development and testing

Start a dedicated local broker from the repository root:

```sh
docker run -d --rm --name commander-rabbitmq-test \
  -p 127.0.0.1:5672:5672 \
  -e RABBITMQ_DEFAULT_USER=commander \
  -e RABBITMQ_DEFAULT_PASS=commander-test \
  --health-cmd 'rabbitmq-diagnostics -q ping' \
  --health-interval 5s --health-timeout 5s --health-retries 20 \
  rabbitmq:4.2.9-management-alpine
```

Wait until `docker inspect --format '{{.State.Health.Status}}'
commander-rabbitmq-test` reports `healthy`, then run:

```sh
COMMANDER_TEST_RABBITMQ_URL='amqp://commander:commander-test@127.0.0.1:5672/' \
  go test -race -count=1 -timeout=3m ./providers/rabbitmq/...
go vet ./providers/rabbitmq/...
docker stop commander-rabbitmq-test
```

Integration tests skip without `COMMANDER_TEST_RABBITMQ_URL`. Tests connect to
an existing broker; they do not start Docker. Use a dedicated broker or virtual
host because tests inspect the shared failed-delivery queue and temporarily unbind it
to exercise failure handling. Test inboxes use
unique identities and durable test queues are cleaned up after each case.
CI starts a RabbitMQ service and runs the suite in
[test-provider-rabbitmq.yml](../../.github/workflows/test-provider-rabbitmq.yml).

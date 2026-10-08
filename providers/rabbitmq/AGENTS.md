# Working on the RabbitMQ provider

Inherit [provider guidance](../AGENTS.md). This module implements `comms.Bus`
using AMQP 0-9-1. Keep broker behavior consistent with the
[consumer contract](../../framework/comms/README.md).

## Topology and publication

- Preserve exact topic routing through the durable direct `commander` exchange.
  [`QueueName`](comms/topology.go) derives stable identity from `(Topic, Name)`;
  excluding mode prevents silently changing durability for an existing identity.
- `TransientExclusive` uses exclusive, auto-delete queues; `DurableShared` uses
  retained durable classic queues with competing consumers. Declare subscriptions
  before publishing. Durable inboxes do not establish multi-controller ownership.
- Publications are persistent and mandatory. Keep publication serialized so returns
  and confirmations correlate correctly; return `ErrUnroutable` for a missing route.
  Broker confirmation says nothing about application acceptance.
- A confirmation timeout leaves an uncertain outcome and closes the publisher
  connection so late returns cannot attach to another publication. Do not add an
  automatic publish retry or reconnect that can execute a command twice.
- Preserve caller cancellation and the 30-second upper bound on setup, publication
  and settlement. Keep prefetch bounded; zero selects the default of 16.

## Delivery settlement

- Keep settlement state on each delivery. Repeating its chosen outcome is safe;
  choosing the opposite outcome returns `comms.ErrDeliverySettled`.
- On rejection, confirm publication to `commander.failed` before acknowledging the
  source delivery. Retain source topic/inbox/queue headers for inspection. A lost
  acknowledgement can produce duplicate failure records; do not promise exactly once.
- Malformed messages also go to the failure queue. Do not introduce an automatic
  retry/dead-letter loop. Agent command expiration remains an agent check, not TTL.
- Shutdown must unblock waiting consumers and close their connections. A host must
  recreate the bus and subscriptions after connection loss.

## Validation

Start the dedicated test broker in [README.md](README.md#development-and-testing)
and wait for health. From the repository root:

```sh
COMMANDER_TEST_RABBITMQ_URL='amqp://commander-test:commander-test-secret@127.0.0.1:5673/' \
  go test -race -count=1 -timeout=3m ./providers/rabbitmq/...
go vet ./providers/rabbitmq/...
```

Integration tests skip without `COMMANDER_TEST_RABBITMQ_URL`. Use a dedicated
broker or virtual host because failure tests alter shared failed-queue bindings.
Stop the test broker even after failures. Exercise connection loss, cancellation,
unroutable publication and independent settlements when changing transport code.
See the [integration CI](../../.github/workflows/test-integration.yml).

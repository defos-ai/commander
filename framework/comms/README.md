# Message handling

`Consumer.Consume` receives messages from a subscription and passes them to a
handler in order by default. It owns the receive, handling, logging, and acknowledgement loop.
The host owns the context used to stop the loop and closes the subscription.

Domain consumers expose `Consume(ctx, message) error`. Dispatchers select concrete
consumers, which decode the payload and complete its domain processing:

| Domain consumer | Incoming messages |
| --- | --- |
| [Agent `ActionConsumer`](../agent/consumers/action_consumer.go) | `action.run` |
| [Agent `DiscoveryConsumer`](../agent/consumers/discovery_consumer.go) | `agent.discover` |
| [Agent `StopConsumer`](../agent/consumers/stop_consumer.go) | `agent.stop` |
| [Agent `ResultReceiptConsumer`](../agent/consumers/result_receipt_consumer.go) | `action.result.accepted` |
| [Controller `AgentEventConsumer`](../controller/consumers/agent_event_consumer.go) | `agent.started`, `agent.heartbeat`, `agent.updated`, `agent.stopped` |
| [Controller `CommandResultConsumer`](../controller/consumers/command_result_consumer.go) | `action.succeeded`, `command.failed` |
| [Controller adapter consumer](../controller/consumers/adapter/adapter_consumer.go) | `adapter.request`, `adapter.request.canceled` |

Domain consumers connect to transport with
`consumer.Consume(ctx, domainConsumer.Consume)`. The controller's
[`EventConsumer.Consume(ctx)`](../controller/consumers/event_consumer.go) wires its
subscription, error policy, and domain consumers; the host starts and stops it.
Filters return nil to skip and acknowledge an irrelevant message. An unsupported
command or event returns an error. `CommandConsumer` and `EventConsumer` contain
the routing switches. The concrete consumers own payload validation, inventory updates, result persistence,
and replies. They receive the stores, bus, and runtime operations they need.
The hosts wire those dependencies and manage startup and shutdown.

See [message contracts](messages/README.md) for routing, payloads, correlation,
and the result-receipt exchange.

| Handler result | Policy |
| --- | --- |
| `nil` | ACK after the handler finishes. |
| An error | Reject the message. Stop unless `ContinueOnError` allows consuming the next delivery. FS keeps the rejected delivery unacknowledged. |

Handlers have the signature `func(context.Context, messages.Message) error`.

By default, any error stops consumption. `ContinueOnError` applies only to handler
errors; receive errors, ACK failures, and cancellation always stop the loop.
Cancellation prevents acknowledgement when observed before ACK. The consumer logs
message and work identifiers without logging payloads or handler error text.

`AfterAck` runs local bookkeeping only after a successful acknowledgement. It
does not run when handling or acknowledgement fails.

## Concurrent requests

`Concurrency` above one lets independent handlers run together. The limit also
bounds deliveries waiting for acknowledgement. A long action request can run
while the controller accepts another command. Handlers must support concurrent
calls and honor cancellation; callbacks remain serialized.

Because `Ack(ID)` covers all delivered copies of an ID, concurrent consumption
waits until every copy succeeds before acknowledging that group. If any copy is
rejected, the group and later copies of that ID stay unacknowledged for this
consumer's lifetime. `AfterAck` runs for each successful delivery after its group
is acknowledged. A fatal failure cancels active work and waits for handlers and
the pending receive to stop before returning.

## Handling versus execution

A nil handler error means processing is complete. An action permission denial can
return nil after its failure response is published. Deliberately ignored
notifications can also return nil without changing application state.

If result publication fails, the command handler waits for the controller to
accept its cached result while background delivery retries without repeating the
action. The handler then returns nil and the consumer acknowledges the command.
Artifact persistence failures return errors and retain their commands, even when
the controller accepts the failure result. Conflicting command identities also
return errors.

The [result-receipt protocol](messages/README.md#correlation-and-acknowledgements)
defines when the controller confirms persistence to an agent.

## Completing work through handlers

A stop command's handler drains pending results, ends background work and waits
for it to finish, then publishes the final stopped announcement. The consumer
acknowledges the command when the handler returns nil; `AfterAck` ends the receive
loop. If publication fails or the request was cancelled, the stop command remains
unacknowledged.

The receipt handler validates the confirmation against the saved result. After
the receipt consumer acknowledges it, `AfterAck` marks the outcome accepted and
unblocks any command handler waiting for result acceptance. The receipt and
command have independent broker acknowledgements. On this recovery path, the
receipt is acknowledged first; a later command ACK failure retains the command
without undoing the receipt.

The consumer does not rerun handlers, deduplicate messages, or schedule retries.
`ContinueOnError` controls whether processing continues: invalid controller
events are rejected while later events continue; controller database or ACK
failures stop the service and fail waiting callers. Agents keep consuming after
action message errors so cached result delivery can recover. ACK failures stop the agent.

For filesystem retention and redelivery limits, see the
[provider notes](../../providers/README.md#filesystem-behavior).

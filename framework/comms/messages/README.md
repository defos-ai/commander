# Message contracts

This package owns the common envelope, wire types and routing helpers.
Typed commands/events and publishers live in [`comms/clients`](../clients), grouped
by recipient. Shared payloads live in `framework/domain`.

The direction of control is **Commander → Controller → Agent**. MCP forwards
operation commands, while queries read stores. Only the controller dispatches to
an agent instance. Agent results and status flow back to the controller.

## Destinations

Destinations are exact topic names. Comms selects the default controller route
for adapters and agents.

| Publisher → consumer | Destination | Messages |
| --- | --- | --- |
| Adapter → controller | `controllers.default.commands` | `adapter.request`: method, serialized command and deadline |
| Adapter → controller | `controllers.default.cancellations` | `adapter.request.canceled` |
| Controller → adapter | `adapters.<adapter-id>.replies` | `controller.response`: result or domain error |
| Controller → agent | `agents.<agent-id>.commands` | `action.run`, `agent.discover`, `agent.stop` |
| Controller → agent | `agents.<agent-id>.events` | `action.result.accepted` |
| Agent → controller | `controllers.default.events` | Lifecycle events, `action.succeeded`, `command.failed` |

Use [`routing.go`](routing.go) to build destinations. Hosts use transient inboxes;
see [inbox lifetimes](../README.md#delivery-guarantees).

## Payloads

Find typed payloads under [`clients`](../clients/), grouped by recipient. The
[`Message`](message.go) envelope carries identity, correlation, routing and time.
Agent snapshots describe readiness and current work; action results carry
structured output and artifact metadata, never file bytes.

Adapter requests include a deadline. Controller-to-agent commands carry
`expires_at`, the latest time new work can start. Cached outcomes remain
replayable after that time; see
[admission deadlines](../README.md#command-admission-deadlines).

## Correlation and acknowledgements

Replies set `correlation_id` to the command ID. Action commands, outcomes and
receipts carry the same operation/task `work` context. Adapters subscribe before
publishing and do not automatically retry mutations after a lost reply.

```mermaid
sequenceDiagram
    participant C as Controller
    participant D as Database
    participant A as Agent
    C->>D: Reserve command ID
    C->>A: action.run
    A->>A: Execute, upload files, cache outcome
    A->>C: action.succeeded / command.failed
    C->>D: Save result and artifact metadata
    C->>A: action.result.accepted
    A->>A: Stop result retries
```

The receipt matches the result ID, command ID and work context. Outcome replay
can repair incomplete metadata persistence without rerunning the action. Command
deduplication and pending results last only for the agent process's lifetime.

`Delivery.Ack(ctx)` separately confirms successful processing of one delivery;
`Delivery.Reject(ctx)` records unsuccessful processing. Neither replaces the
controller's durable result receipt.

See [consumer behavior](../README.md) and
[restart recovery](../../../docs/overview.md#state-and-restart-recovery) for limits
on replay and unknown outcomes.

For changes to these contracts, see [coding guidance](AGENTS.md).

## Secret synchronization contracts

These contracts prepare encrypted delivery; runtime hosts do not publish or
consume them yet. Secret declarations alone do not make an action ready.

| Direction | Message | Correlation |
| --- | --- | --- |
| Agent → controller events | `secret.sync.requested` | Message ID is the pending request ID |
| Controller → agent events | `secret.delivered` | Request ID |
| Agent → controller events | `secret.installed` | Delivery message ID |

A request identifies one scoped secret and its recipient. Delivery contains an
encrypted secret and expires at the earlier of the request deadline or secret
expiry. An installation acknowledgement identifies the request, secret ID and
version; send it only after successful decryption and installation. These
messages carry no operation/task work context.

Typed validators match deliveries to requests and acknowledgements to the exact
delivery. Runtime consumers must additionally authorize the sender and scope,
check the active request and current stored secret version, and reject expired
deliveries when received. Broker acknowledgement does not fulfill a secret
request, and historical installation does not prove ongoing readiness. Refresh
and revocation reconciliation are not implemented yet.

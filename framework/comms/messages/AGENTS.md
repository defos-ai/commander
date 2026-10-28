# Working on wire messages

- Keep [`message.go`](message.go) limited to the shared envelope, constructors,
  validation and work context; [`types.go`](types.go) owns message type names and
  [`routing.go`](routing.go) owns destination builders. Typed recipient payloads
  belong in [`../clients`](../clients/), not this package.
- Use routing helpers for exact topic names. Agent commands and result receipts
  use separate subscriptions so long commands cannot delay acceptance receipts.
- Preserve envelope validation: UUID message/correlation IDs, valid runtime IDs,
  object payloads and timestamps. Adapter messages have an adapter ID with no
  agent or work context; agent messages have an agent ID with no adapter ID.
- A nonzero work context requires operation and task IDs. Preserve this context
  across commands, outcomes and receipts; replies correlate to the command ID.
- Result receipts match result ID, command ID and work context and follow durable
  controller persistence. They must never be treated as broker acknowledgements.
- Checkpoint requests correlate to the original action command; responses
  correlate to the checkpoint request and retain command/name/work identity.
  Validate bounded JSON and fixed error codes in the typed clients. The controller
  must match the saved execution claim before reading or writing; an inventory
  lease is not ownership evidence. Saves acknowledge persisted data, and identical
  request retries must not rerun actions. Use the independent agent event inbox.
- Execution logs use [`DecodeExecutionLog`](../clients/controller/events/logs.go)
  to match envelope ID, agent, command correlation, work and timestamp against the
  record. Keep the 2048-byte message bound and 64 KiB encoded payload bound; JSON
  escaping may expand the message. Logs have no admission expiry or receipt.
  The controller verifies the saved command and action before persisting, including
  delayed logs for completed claims. Reject failed log processing while continuing
  other events; receive and settlement failures still stop the consumer.
- Secret synchronization uses the controller owner loop and agent event inbox.
  Keep secret deliveries separate from action commands. The controller decrypts
  encrypted reservations; wire payloads carry metadata and value bytes, never
  the storage ciphertext or encryption key. Agents do not decrypt. Match requests,
  recipient identities/types, shared names, deadlines and exact secret IDs/versions
  through the typed validators.
  Consumers must separately verify the live registered recipient, active requests,
  receipt-time expiry and current stored versions before installation/fulfillment.
  Broker/filesystem access is the trusted publisher boundary; envelope fields do
  not authenticate an untrusted publisher.
  An installation acknowledgement never proves ongoing readiness or revocation.
- Preserve `expires_at` as a start deadline. Cached outcomes can replay after
  expiry; new command work cannot. No expiry means no admission limit.
- When changing a wire field or message type, update its typed clients, producing
  and consuming code, validation and recovery together. Reflect protocol behavior
  in the [message contract](README.md) without duplicating payload inventories.

## Validation

Run from the repository root:

```sh
go test -race -count=1 -timeout=3m ./framework/...
go vet ./framework/...
```

For changed wire behavior, also run affected provider and public API/MCP suites
using the [testing guide](../../tests/README.md).

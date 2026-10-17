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
- Secret synchronization uses the controller owner loop and agent event inbox. Keep encrypted
  deliveries separate from action commands. Match requests, recipients, scoped
  keys, deadlines and exact secret IDs/versions through the typed validators.
  Consumers must separately verify scope authorization, active requests,
  receipt-time expiry and current stored versions before installation/fulfillment.
  Broker/filesystem access is the trusted publisher boundary; envelope fields do
  not authenticate an untrusted publisher. No cross-type grants are enabled.
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

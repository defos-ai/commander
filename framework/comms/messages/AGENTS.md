# Working on wire messages

- Keep [`message.go`](message.go) limited to the shared envelope, constructors and
  validation, including explicit `OperationID` and `TaskID` fields.
  [`types.go`](types.go) owns message type names and
  [`routing.go`](routing.go) owns destination builders. Typed recipient payloads
  belong in [`../clients`](../clients/), not this package.
- Use routing helpers for exact topic names. Agent commands and result receipts
  use separate subscriptions so long commands cannot delay acceptance receipts.
- Preserve envelope validation: UUID message/correlation IDs, valid runtime IDs,
  object payloads and timestamps. Adapter messages have an adapter ID with no
  agent, operation or task IDs; agent messages have an agent ID with no adapter ID.
- Operation and task IDs must both be empty or both be canonical UUIDs. Preserve
  both fields across commands, outcomes and receipts; replies correlate to the
  command ID. Shared-data sources require both IDs.
- Result receipts match result ID, command ID, operation ID and task ID and follow durable
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
  Reusable synchronization can carry a default of at most 64 KiB, with nil
  meaning absent and nonnil empty bytes meaning an empty default. The typed
  payload redacts formatting; decoders and validation callers clear owned
  defaults. Persist only request metadata and encrypted shared values, and keep
  default bytes out of schemas, questions, logs and model context. Transport
  retries retain the original request identity, payload and expiry.
  Transient requests reuse synchronization with exact command/work binding and
  their original fixed deadline. Values use separate transient responses; never
  decode them as reusable metadata or install them in the shared cache. A receipt
  correlates to the exact value delivery and confirms receipt only, not external
  acceptance. Cancellation retains the original request metadata and is valid
  after expiry. Error responses carry no value and may report expiry after the
  deadline. Keep value bytes bounded and clear decoded copies after validation.
- Execution question requests use command correlation plus a stable key; replies
  correlate to the individual request ID and preserve command/key/work identity.
  The independent agent event inbox delivers pending or answered saved questions
  while the action waits. The controller validates its saved execution claim and
  active task before saving or returning a question. Idempotent polls never reset
  fallback times or repeat action effects. Keep question content and answers
  separate from secret payloads; canceled execution waits reject late replies.
- Preserve `expires_at` as a start deadline. Cached outcomes can replay after
  expiry; new command work cannot. No expiry means no admission limit.
- [`DataShared`](../clients/agent/events/sharing.go) uses the agent event inbox,
  targeting an exact recipient command, operation and task. Decode at most 256 KiB
  and require another source task in the same operation. Preserve existing source
  identities, artifact ownership and answered-question origin; metadata does not
  prove acceptance or grant new authority. Share selected output data without
  follow-up proposals, session mutations, file bytes or secret payloads. The
  receiving runtime still matches active execution/lifetime independently.
  Controller forwarding follows durable result/artifact acceptance and the saved
  operation policy; current recipient task, claim and observed work must agree.
  Task-scoped decisions follow successful answer saves, retaining their original
  question IDs and operator/model provenance. Broker ACK is not action consumption.
  Batch lifecycle payloads use a controller source with the operation and parent
  task IDs and no agent/command/action identity. Producer updates retain their agent source and
  may carry a batch UUID. Require unique expected member IDs, exclude both parent
  and recipient, and accept only known outcomes for every member at finish.
  Deep-clone membership/outcomes. Keep the existing 256 KiB encoded limit and
  reject oversized groups rather than truncating them. Lifecycle metadata is
  descriptive; trusted bus access remains the publisher authorization boundary.
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

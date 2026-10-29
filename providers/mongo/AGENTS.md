# Working on the MongoDB provider

Inherit [provider guidance](../AGENTS.md). This module stores database records;
artifact bytes and reports belong to the separate storage provider.

## Client and persisted records

- Keep client creation/configuration in [`store.Open`](store/mongo_store.go).
  Require a database name and support `mongodb://` and `mongodb+srv://` through
  driver validation, including seed lists. Share one `MongoStore` across domain
  stores; the host closes it after consumers stop.
- Keep driver initialization errors safe to print in `store.Open`; URI options
  can contain secrets. Preserve the underlying cause for `errors.Is`/`errors.As`,
  but do not log that cause or duplicate redaction in individual hosts.
- Preserve primary reads, majority read/write concerns and snapshot transactions.
  Operation storage requires a replica set or sharded cluster; do not fall back
  to partially applied writes on standalone MongoDB.
- Keep MongoDB documents, BSON conversions and query/index details local. Use the
  existing document mappings where domain values need custom timestamp handling.
- Request-derived IDs stay strings through `GetByID`. Build BSON keys and
  operators in provider code; never decode client input into a query document.
  JSON or operator-looking strings are literal values, not query syntax.
- Mission creation uses an atomic primary-key insert and rejects every existing
  name, including an identical definition. Keep seeding's `Upsert` replacement
  behavior separate; only a duplicate `_id` maps to `missions.ErrMissionExists`.
- Operations span metadata, task, question and event collections. Initialize
  collections/indexes before the first transaction; preserve unique positions and
  event keys within each operation.
- In operation mutations, reconstruct state from the latest transaction snapshot
  on every attempt, including generated child IDs and event sequences. Check the
  stored revision when replacing metadata; update related collections together.
- Store `completion_draft` in the operation JSON body, preserving raw JSON number
  tokens and escapes. `SaveCompletionDraft` validates the latest transactional state;
  identical submissions skip writes, while accepted completion clears the field
  atomically with output, report reference and completion origin.
- Execution checkpoints are immutable named JSON values, stored as BSON binary
  to preserve number precision. Read and update their map inside one transaction;
  concurrent checkpoint writes or completion must retry against current state.
  Return failed commit confirmation to the caller for reconciliation.
- [`ExecutionMongoStore`](executions/logs.go) keeps immutable logs in separate
  `execution_logs` documents. JSON binary preserves timestamp nanoseconds and
  message bytes; validate ID and indexed provenance against the decoded payload.
  Allocate each positive position from `execution_log_counters` and insert the log
  in one transaction: publishing the counter first could let live readers skip
  a delayed insert. Identical retries retain their original position; conflicting
  content fails, and uncertain commit errors reach the caller for reconciliation.
  Logs never enlarge or rewrite execution records.
- Initialize log collections/indexes and the counter before the first transaction.
  Reject unpositioned existing records or a counter inconsistent with the saved
  tail; do not backfill, reset or delete data. [`ListLogs`](executions/log_queries.go)
  resolves `AfterLogID`, captures the head and reads matching positions in one
  snapshot, applying exact filters and the requested limit before decoding.
  Empty/default `asc` reads positions above the cursor in ascending order;
  `desc` reads below the cursor in descending order, starting at the saved head
  when no cursor is supplied. Apply the requested sort and limit in MongoDB;
  do not reverse or sort the returned page in memory.
  The cursor must identify a committed log matching every supplied
  filter; otherwise return `ErrUnknownLog`. Keep simple collation on
  equality/position indexes and queries. Return a nonnil slice on success;
  callers continue after the last returned log ID and retain their cursor on an
  empty result. Producer timestamps and UUIDs never determine ingestion order.
- Keep [`operationWriteError`](operations/operation_mongo_store.go) conservative
  about ambiguous commits. Do not turn an unknown commit outcome into a definite
  rollback or discard `operations.ErrWriteUncertain` while wrapping errors.
- [`SecretMongoStore`](secrets/secret_mongo_store.go) retains every secret UUID in
  `secrets`; deletion drops its encrypted payload and marks the identity inactive.
  A partial unique index reserves shared secret names only for active records. Use simple
  collation for case-sensitive names and never TTL-delete expired or retired IDs.
  JSON payloads preserve authenticated nanoseconds and uint64 versions; validate
  their identity against indexed fields. The store never decrypts or loads keys.
  Preserve transaction error labels and reset attempt state on retries. Retry a
  duplicate-key abort with a fresh snapshot to recognize identical concurrent
  requests; failed commit confirmation after a write is `secrets.ErrWriteUncertain`.
- [`RequestMongoStore`](secrets/request_mongo_store.go) stores one JSON aggregate
  per request in `secret_requests`, validating indexed ID/status/delivery ID
  against its payload. Keep request and delivery IDs permanent; the partial unique
  delivery index spans terminal records too. Mutations use domain record helpers
  inside a transaction over the latest record. Pending recovery includes overdue
  requests until explicitly expired. Preserve ciphertext after settlement and
  primary-secret deletion; historical installation evidence is not readiness.
  Never decrypt, publish, or read the primary secret store to settle a request.
- Test cleanup removes operation records and retires artifacts with private
  deletion markers. Preserve collections, indexes, catalogs and execution records;
  a delayed accepted-result write must not recreate deleted artifact metadata.

- [Session scopes](sessions/session_mongo_store.go) are transactional JSON
  aggregates in `runtime_sessions`, preserving exact timestamps and revisions.
  Keep collection creation outside transactions and release/history in the same
  document. Retain identities without TTL cleanup; preserve uncertain commits.
  Lifecycle validation uses the isolated [public E2E suite](../../docs/runtime-sessions.md#e2e-checks).
  Discovery narrows scope reads by `_id` when supplied and filters decoded records
  in the provider. Keep query criteria as typed values, include retained history,
  and order results by scope ID, then session ID.

## Validation

Use a dedicated test database on a running replica set. Follow
[service setup](README.md#development-and-testing); tests clean their collections before and
after cases. Run from the repository root:

```sh
COMMANDER_TEST_MONGO_URL='mongodb://127.0.0.1:27017/commander_mongo_tests?replicaSet=rs0&directConnection=true' \
  go test -race -count=1 -timeout=3m ./providers/mongo/...
go vet ./providers/mongo/...
```

Integration tests skip unless `COMMANDER_TEST_MONGO_URL` is set. It also enables
the optional controller startup test; run that affected downstream check when
changing initialization. Preserve transaction rollback, concurrent mutation and
uncertain-write coverage when changing operation persistence. See the
[integration CI](../../.github/workflows/test-integration.yml) and
[stack test guide](../../framework/tests/README.md) for broader checks.

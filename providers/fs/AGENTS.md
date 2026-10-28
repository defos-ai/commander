# Working on the filesystem provider

Inherit [provider guidance](../AGENTS.md). This module implements the bus,
database stores, artifact files and reports. See [README.md](README.md) for use.

## Files and operation snapshots

- Keep connection parsing in [`FilesystemPath`](path.go); it resolves paths
  without opening or creating resources. Constructors take filesystem paths.
- Reuse `internal/jsonfile` for locking and publication. Shared filesystems must
  support file locks, atomic renames and hard links; process-local locking alone
  cannot protect stores shared by hosts.
- Mission creation checks names and writes under one catalog lock, rejecting an
  existing name even for an identical definition. Keep startup `Upsert` separate;
  callers reconcile failed writes by reading the saved definition.
- An operation's manifest and sibling `.json.records` directory are one saved
  snapshot. Use [`Records`](internal/jsonfile/records.go) to publish immutable,
  digest-checked record bodies through one manifest; never edit committed blobs.
- Keep file and directory sync ordering intact. Failures before manifest publication
  leave the old snapshot authoritative; a published manifest whose directory sync
  failed returns `ErrWriteUncertain`. An identical retry still confirms durability.
- `Records.View` reads without a writer lock and may rerun its entire callback if
  collection removes an old blob. Do not retain `RecordView` outside the callback,
  mix snapshots or accumulate partial results across attempts.
- Preserve regular-file and directory checks, including symlink rejection where
  required by `jsonfile` and cleanup. Keep persistent locks when removing test data.
- JSON collection reads and transactions pin a directory with `os.Root`; staging,
  rename and blob cleanup stay relative to that handle. Keep final file opens
  non-following and release scoped handles even when callbacks fail. Inject sync
  failures after root acquisition when testing uncertain publication.
- Provider roots and their ancestors are trusted host configuration. Scoped roots
  constrain one read or transaction; they do not preserve ancestor identity between
  operations. Keep those directories inaccessible to untrusted local writers.
- [`SecretFsStore`](secrets/secret_fs_store.go) keeps encrypted secret records and
  retired IDs in one `secrets.json` collection. One lock protects global IDs and
  active agent-type/name pairs; deletion drops ciphertext and retains identity forever.
  Exact retries must still confirm durability. Translate uncertain publication to
  `secrets.ErrWriteUncertain`. Preserve timestamp nanoseconds, compare time instants,
  and never decrypt or load encryption keys here. Secret names never become paths.
- [`RequestFsStore`](secrets/request_fs_store.go) saves request lifecycles in a
  separate `secret-requests.json` collection. One lock protects request IDs,
  globally unique delivery IDs, immutable encrypted delivery snapshots and exact
  installation evidence. Use domain `RequestRecord` transitions; exact retries
  still publish/sync, and initial request retries never reset progress. Pending
  lists include overdue requests until explicit expiration. Terminal records and
  their encrypted deliveries remain retained; deleting a current secret does not
  erase these copies or revoke installed plaintext. This store neither checks the
  current `SecretStore` nor distributes values or performs retention cleanup.
- [`ExecutionFsStore`](executions/execution_fs_store.go) saves named immutable
  checkpoints under the same collection lock as terminal results. Checkpoint
  writes require an existing claim; exact retries still publish/sync, including
  after completion. Use domain transitions so a late checkpoint cannot extend a
  completed execution or overwrite an earlier checkpoint.
- [`LogFsStore`](executions/log_fs_store.go) keeps each immutable observation in
  its own `execution_logs/<UUID>.json` collection, independent of execution state
  and other logs. Identical retries republish the original bytes to confirm
  durability; conflicting IDs never replace saved provenance or content. Reads
  create no state. The controller verifies the saved execution claim before append.

- Session persistence uses [scope aggregates](sessions/session_fs_store.go) under
  one collection lock. Reserve/release and retained identity evidence commit
  together; uncertain writes retain `sessions.ErrWriteUncertain`. Reconcile
  operation and execution stores in the framework coordinator, outside this lock.
  Discovery filters records inside a collection view, preserving retained history
  and returning independent records ordered by scope ID, then session ID.

## Bus lifecycle

- Support only `TransientExclusive`; reject `DurableShared` with
  `comms.ErrUnsupportedInbox`. `(Topic, Name)` identifies an exclusive live inbox.
- Subscriptions begin at the topic tail. Publication snapshots active recipients;
  the last acknowledgement removes a file only when none rejected it.
- Registration, publication and settlement share the topic lock. Track settlement
  immediately after rename/removal; a sync failure must not permit the opposite
  outcome. A repeated settlement retries durability without settling another copy.
- A lost lease ends the inbox; do not silently rejoin a cursor that missed messages.
  Closing keeps unsettled/rejected files for inspection. Reopening starts fresh;
  do not introduce replay, automatic redelivery or subscription recovery.

## Validation

Run from the repository root; tests use local temporary directories and need no
external service:

```sh
go test -race -count=1 -timeout=3m ./providers/fs/...
go vet ./providers/fs/...
```

Changes to publication or locks need failure, cancellation and concurrent-access
coverage in `tests/`; operation changes also need snapshot/recovery scenarios.
Use the [test guide](../../framework/tests/README.md) for stack checks and
[unit CI](../../.github/workflows/test-unit.yml) for the module gate.

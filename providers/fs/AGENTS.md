# Working on the filesystem provider

Inherit [provider guidance](../AGENTS.md). This module implements the bus,
database stores, artifact files and reports. See [README.md](README.md) for use.

## Files and operation snapshots

- Keep connection parsing in [`FilesystemPath`](path.go); it resolves paths
  without opening or creating resources. Constructors take filesystem paths.
- Reuse `internal/jsonfile` for locking and publication. Shared filesystems must
  support file locks, atomic renames and hard links; process-local locking alone
  cannot protect stores shared by hosts.
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

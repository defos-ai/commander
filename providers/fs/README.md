# Filesystem provider

## Overview

`commander/providers/fs` implements the message bus, all database stores,
artifact files, and reports using local or shared directories. It powers the
[default local stack](../../README.md#quick-start).

Hosts accept local paths, `~/path`, `file:///path`, and `fs:///path`.
[`fs.FilesystemPath`](path.go) resolves these to absolute paths without opening
resources; concrete constructors take paths. Shared filesystems must support
file locks, atomic renames, and hard links.

## Quick start

From the repository root:

```sh
docker compose up --build -d
```

To embed a store, import the domain package you need. The workspace already
includes the module; private projects use
[local module replacements](../../apps/agents/README.md#develop-an-agent-in-a-private-repository).
For example, list missions from a filesystem database:

```go
package queries

import (
    "context"

    "commander/framework/domain/missions"
    "commander/providers/fs"
    fsmissions "commander/providers/fs/missions"
)

func ListMissions(ctx context.Context, databaseURL string) ([]missions.Mission, error) {
    root, err := fs.FilesystemPath(databaseURL)
    if err != nil {
        return nil, err
    }
    store, err := fsmissions.NewMissionFsStore(root)
    if err != nil {
        return nil, err
    }
    return store.List(ctx)
}
```

| Package | Constructors | Host setting |
| --- | --- | --- |
| [`comms`](comms/) | `NewFsBus` | `BUS_URL` |
| [`artifacts`](artifacts/) | `NewArtifactFsStore` for metadata; `NewArtifactFileFsStore` for bytes | `DATABASE_URL`; `STORAGE_URL` |
| [`reports`](reports/) | `NewReportFsStore` | `STORAGE_URL` |
| Other domain packages | `NewActionFsStore`, `NewAgentFsStore`, `NewExecutionFsStore`, `NewMissionFsStore`, `NewOperationFsStore` | `DATABASE_URL` |

Close the bus after consumers stop. Filesystem stores need no close.

## Development and testing

Run from the repository root:

```sh
go test -race -count=1 -timeout=3m ./providers/fs/...
go vet ./providers/fs/...
```

CI runs these in [test-provider-fs.yml](../../.github/workflows/test-provider-fs.yml).
See the [testing guide](../../framework/tests/README.md) for shared conventions
and framework/app checks.

### Delivery and recovery

FS supports only `comms.TransientExclusive` inboxes. Requesting `DurableShared`
returns `comms.ErrUnsupportedInbox`. Inbox identity is `(Topic, Name)`; a second
active owner receives `comms.ErrInboxInUse`. Distinct names on the same topic
receive independent copies.

Subscriptions start at the current topic tail and must be live before publication.
Closing or losing the owner ends the inbox; reopening its name starts fresh.
`Next` returns a delivery handle with `Message()`, `Ack(ctx)` and `Reject(ctx)`.
Each publication settles independently, including copies sharing a message ID.
A message file is deleted only after all subscriptions registered at publication
acknowledge their delivery.

Rejecting a delivery retains its file as evidence without requeueing it. Repeating
the same settlement on a handle is harmless; the opposite settlement returns
`comms.ErrDeliverySettled`. Settlement retries use that handle, without a bounded
message-ID acknowledgement history.

Closing or losing a subscription retains unsettled and rejected files for
inspection. There is no automatic redelivery, subscription recovery, or dead-letter
queue. Reopening the bus does not replay old commands. See the
[message-handling guide](../../framework/comms/README.md) for consumer settlement policy.

### Agent observations

`DATABASE_URL/agents/<agent-id>.json` stores the latest source observation.
Inventory writes and expiry share a file lock: equal or older observations cannot
replace a saved snapshot. Expired records disappear from queries; a private marker
remains only while its observation time is within the heartbeat window, then is
removed. The same file protects ordering across store or controller restarts.

### Atomic operation records

`DATABASE_URL/operations/<operation-id>.json` is a versioned manifest with a
writer lock for that operation. Metadata, tasks, questions, and events live in
content-addressed JSON files under `<operation-id>.json.records/`. One atomic
manifest replacement commits related changes together and reuses unchanged
records. Readers assemble consistent snapshots without a writer lock.

[`jsonfile.Records`](internal/jsonfile/records.go) stages and syncs record files
before publishing the manifest. Staging failures leave the saved snapshot
unchanged. An unconfirmed directory sync after publication returns
`ErrWriteUncertain`; an identical retry confirms durability without a new
revision. Later writes collect abandoned records. Providers must prepare the
manifest's parent directory durably before opening a record set.

Task and event queries read metadata and selected bodies without loading
unrelated results, questions, or history. `Records.View` retries the entire read
on a newer snapshot if collection removes an old record; callbacks must avoid
external effects. Full operation reads and mutations still assemble the full
operation. Cursor and transition semantics are defined by
[`OperationStore`](../../framework/domain/operations/operation_store.go).

### Artifact files and reports

Accepted metadata lives at `DATABASE_URL/artifacts/<artifact-id>.json`; bytes
live at `STORAGE_URL/artifacts/<artifact-id>/<file-id>`. Reports live at
`STORAGE_URL/reports/<report-id>.json`. Synced temporary files are published with
exclusive hard links, preventing partial files or replacement of existing bytes.
Artifact reads verify size and SHA-256. Repeating a report write accepts identical
content and rejects a conflicting report.

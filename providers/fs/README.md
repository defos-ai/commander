# Filesystem provider

## Overview

`github.com/defos-ai/commander/providers/fs` implements the message bus, all
database stores, artifact files, and reports using local or shared directories. It powers the
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
For example, open a mission store:

```go
import (
    "github.com/defos-ai/commander/providers/fs"
    fsmissions "github.com/defos-ai/commander/providers/fs/missions"
)

root, err := fs.FilesystemPath(databaseURL)
if err != nil {
    return err
}
missionStore, err := fsmissions.NewMissionFsStore(root)
```

Other constructors live in their domain directories; see [`comms`](comms/),
[`artifacts`](artifacts/) and [`reports`](reports/) for bus and file storage.
Close the bus after consumers stop. Stores need no close.

## Development and testing

See [AGENTS.md](AGENTS.md) for filesystem and message-delivery invariants.

Run from the repository root:

```sh
go test -race -count=1 -timeout=3m ./providers/fs/...
go vet ./providers/fs/...
```

See the [testing guide](../../framework/tests/README.md) for full-stack checks.

## Delivery and recovery

FS supports only `comms.TransientExclusive` inboxes. Requesting `DurableShared`
returns `comms.ErrUnsupportedInbox`. Inbox identity is `(Topic, Name)`; a second
active owner receives `comms.ErrInboxInUse`. Distinct names on the same topic
receive independent copies.

Subscriptions start at the current topic tail and must be live before publication.
Closing or losing the owner ends the inbox; reopening its name starts fresh.
A message file is deleted only after all subscriptions registered at publication
acknowledge their delivery.

Closing or losing a subscription retains unsettled and rejected files for
inspection. There is no automatic redelivery, subscription recovery, or dead-letter
queue. Reopening the bus does not replay old commands. See the
[message-handling guide](../../framework/comms/README.md) for consumer settlement policy.

## Atomic operation records

Each operation uses a manifest at `DATABASE_URL/operations/<operation-id>.json`
and data in `<operation-id>.json.records/`. Together they form one saved operation;
related changes commit atomically. See [`jsonfile.Records`](internal/jsonfile/records.go)
and the [`OperationStore` contract](../../framework/domain/operations/operation_store.go)
for persistence and recovery behavior.

## Artifact files and reports

Accepted metadata lives at `DATABASE_URL/artifacts/<artifact-id>.json`; bytes
live at `STORAGE_URL/artifacts/<artifact-id>/<file-id>`. Reports live at
`STORAGE_URL/reports/<report-id>.json`. Artifact reads verify size and SHA-256.
Repeating a report write accepts identical content and rejects a conflicting report.

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

Mission `Create` validates the definition and atomically rejects existing names
with `missions.ErrMissionExists`. Use `Upsert` for startup seeding that may replace
an existing definition. Operations retain their own mission snapshots.

## Encrypted secrets

[`secrets.NewSecretFsStore(root)`](secrets/secret_fs_store.go) stores encrypted
records in `root/secrets.json`. Encrypt values with
[`crypto.SecretCipher`](../../framework/crypto/README.md) before saving them;
the store does not need the encryption key. Reads preserve timestamp precision
so records can be authenticated after reopening.

Updates check the saved version. Deletion removes the encrypted value and retains
its ID to reject delayed writes. These records are independent of operations and
are preserved by test cleanup. Use the [secret management service](../../framework/secrets/README.md)
or the authenticated [HTTP API](../../apps/adapters/api/README.md#secrets) to manage values.

Secrets are identified by agent type and name. Earlier development records with
profiles are incompatible; recreate those secrets in a fresh store. Existing
records are never merged or migrated automatically.

[`secrets.NewRequestFsStore(root)`](secrets/request_fs_store.go) saves requests,
their encrypted deliveries and installation acknowledgements in
`root/secret-requests.json`. It preserves progress across restarts and rejects
conflicting delivery or acknowledgement retries. Expiration is explicit.

Terminal requests retain their encrypted delivery snapshots. Deleting a secret
from the secret store does not erase these copies or revoke values already
installed in an agent. The controller uses this store for
[delivery recovery](../../framework/secrets/README.md#request-and-delivery-tracking);
automatic retention cleanup is not implemented.

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

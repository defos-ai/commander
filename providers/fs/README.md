# Filesystem provider

Run multi-agent workflows with filesystem-backed operation state, execution
checkpoints, progress logs and artifact storage, without a separate database or
broker. All-in-one uses this provider automatically.

## Quick start

For custom hosts, set `BUS_URL`, `DATABASE_URL` and `STORAGE_URL` to separate
paths, such as `./state/bus`, `./state/database` and `./state/storage`.
Absolute `file:///` and `fs:///` URLs are also supported.

To embed a mission store:

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

Use `Create` for a new mission; `Upsert` replaces a seeded definition.
For an external project, follow the [local module setup](../../apps/agents/README.md#develop-an-agent-in-a-private-repository).

Keep state directories inaccessible to untrusted writers. Shared filesystems must
support file locks, atomic renames and hard links.

## Encrypted secrets

Use [`NewSecretFsStore`](secrets/secret_fs_store.go) with the
[secret service](../../framework/secrets/README.md). Supply encryption keys to the
service, not the store; back up the key separately from database files.

## Development and testing

From the repository root; no external services are needed:

```sh
go test -race -count=1 -timeout=3m ./providers/fs/...
go vet ./providers/fs/...
```

Use the [testing guide](../../framework/tests/README.md) for cross-host checks.

## Delivery and recovery

Use `TransientExclusive` inboxes; durable inboxes are unsupported. Subscribe before
publishing. A lost lease closes the subscription; reopening receives new
publications only.

Inspect retained rejected or unsettled files when investigating a failure; they
are not automatically replayed. See [messaging](../../framework/comms/README.md).

## Atomic operation records

Back up each operation's `.json` manifest together with its `.json.records/`
directory. Do not edit their contents manually.

## Artifact files and reports

Back up `STORAGE_URL` alongside `DATABASE_URL`: artifact metadata and file bytes
live separately. See [AGENTS.md](AGENTS.md) for storage and recovery constraints.

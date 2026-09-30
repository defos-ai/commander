# Providers

## Overview

Providers implement the framework's messaging and persistence interfaces.
Application hosts select provider modules, open their connection URLs, and inject
them into the runtime, keeping infrastructure details out of agents and controllers.

### Existing providers

| Provider | Implements | Connection formats |
| --- | --- | --- |
| [`fs`](fs/) | Message bus, artifacts, reports, inventory, executions, missions, operations, and action catalogs | Local paths, `~/path`, `file:///path`, `fs:///path` |
| [`mongo`](mongo/) | Mission catalog (first slice) | `mongodb://`, `mongodb+srv://` |

Each provider is a separate Go module: `commander/providers/fs` and
`commander/providers/mongo`. There is no shared `commander/providers` module;
import only the backends your host uses. Providers export concrete stores and
brokers. Each app's `cli/factory.go` maps configured URLs to their constructors
and gives `cli.go` framework interfaces. Each dependency has its own factory:
filesystem inputs select FS, and `mongodb://` or `mongodb+srv://` select Mongo
where implemented. Missing stores and unsupported schemes return explicit errors
without falling back to FS. Only missions support Mongo so far; the full
controller and MCP still require FS until the remaining database stores exist.
Bus factories reserve `amqp://` and `amqps://` for RabbitMQ; artifact and report
factories reserve `s3://` for S3. These backends are not implemented yet.

### Filesystem behavior

The bus retains unacknowledged files for inspection, but does not automatically
redeliver them or provide a dead-letter queue. Subscriptions must be live when
messages are published. Shared filesystems must support file locks, atomic
renames, and hard links. The [message-handling guide](../framework/comms/README.md)
defines consumer acknowledgement policy.

Each `DATABASE_URL/operations/<operation-id>.json` is a manifest with its own
writer lock. Metadata, individual tasks, questions, and events live in
content-addressed files under `<operation-id>.json.records/`. Metadata keeps
ordered task/question IDs and event sequences. One manifest replacement
commits related changes together; unchanged record files are reused. Reads
assemble consistent snapshots without taking a write lock.
`OperationStore.GetTask` queries one task; `ListTasks` pages in assignment order
after a task ID, and `ListEvents` pages after an event sequence. Limits must be
positive. These queries read the manifest, metadata, and selected record bodies;
they do not load unrelated task results, questions, or event history. Full
operation reads and mutations still assemble the complete operation.

## Quick start

For a private project, follow the [local module setup](../apps/agents/README.md#develop-an-agent-in-a-private-repository)
to add `commander/framework` and `commander/providers/fs`. Within this repository,
[go.work](../go.work) already connects the provider modules. Import the filesystem
constructor you need; Mongo has its own [quick start](mongo/README.md#quick-start):

| Constructor | Host setting | Framework interface |
| --- | --- | --- |
| [`comms.NewFsBus`](fs/comms/fs_bus.go) | `BUS_URL` | `comms.Bus` |
| [`artifact.NewArtifactFsStore`](fs/artifact/artifact_fs_store.go) | `STORAGE_URL` | `artifactstore.ArtifactStore` |
| [`reports.NewReportFsStore`](fs/controller/reports/report_fs_store.go) | `STORAGE_URL` | `reports.ReportStore` |
| [`missions.NewMissionFsStore`](fs/controller/missions/mission_fs_store.go) | `DATABASE_URL` | `missions.MissionStore` |

Other database stores have constructors in [`fs/controller/`](fs/controller/).
Filesystem constructors accept paths; [`fs.FilesystemPath`](fs/path.go) converts
configured paths and `file://` or `fs://` URLs without opening resources.

For example, query missions through a filesystem database:

```go
package queries

import (
    "context"

    "commander/framework/domain/missions"
    "commander/providers/fs"
    fsmissions "commander/providers/fs/controller/missions"
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

Hosts read environment variables and flags. For agent artifacts, construct
`artifact.NewArtifactFsStore(storageRoot)`, then call `store.ForAgent(agentID)`
with the same ID as the agent and inject the scoped store. Close brokers and
shared database clients after consumers stop; filesystem stores need no close.
See [configuration](../docs/deployment.md#configuration) for connection settings.

## Development and testing

### Atomic filesystem record sets

`fs/internal/jsonfile.Records` stages immutable JSON records and publishes them with
one versioned manifest. Writers lock only that record set; readers take no lock.
Unchanged records are reused, and later writes collect abandoned files. Only
manifest publication can return `ErrWriteUncertain`; an identical retry confirms
durability without creating another revision. The containing directory must be
prepared durably before opening a record set.

`Records.View` reads selected bodies from one manifest without locking; if
collection removes an older file, it retries the entire callback on a newer
snapshot. The operation provider uses this for task and event queries.

Existing development snapshots use an older format; start with fresh state
rather than migrating them.

### Adding a backend

1. Add a separate module under `providers/<backend>/`, keeping its driver dependencies local (see `mongo/`).
   Implement interfaces from `framework/comms`, `framework/artifact`, or
   `framework/domain`; providers must not depend on application runtimes.
2. Export concrete constructors. The app's `cli/factory.go` selects and constructs
   its providers, shares connections between stores, and returns framework interfaces.
   Providers own connection mechanics; the app closes resources after consumers stop.
3. Preserve the contracts for message acknowledgement, artifact ownership,
   immutable reports, execution outcomes, and atomic operation updates.
   `OperationStore` exposes explicit transitions against the latest saved operation;
   locks and storage revisions stay inside the provider.
   `RefreshPreflight` assesses the saved mission against an inventory snapshot,
   skipping unchanged readiness. `Recover` validates saved work and settles due
   questions before returning the operation used to restore tasks and artifacts.
   `QueueTask` saves a direct assignment with its event and status update;
   retries with the same task ID preserve saved progress without another write.
   `DispatchTask` atomically claims an eligible queued task and saves its agent,
   timing, dispatch event, and operation status together, preserving its command ID.
   `FinishTask` saves the result, continuation, automatic follow-ups, and events
   together. Identical results return the saved state without rewriting it;
   conflicting outcomes cannot replace a final result.
   `ResolveContinuation` checks the decision version and saves its follow-up tasks
   and events atomically; a stale version cannot create a second branch.
   Question methods save their linked decision, status, and events together.
   `AnswerQuestion` commits expiration instead of a late answer; callers inspect
   the returned question status. `ExpireQuestions` skips writes when nothing is due.
   `Complete` rechecks completion eligibility and saves output, the report reference,
   and resolved model decisions together. The caller publishes the immutable report first.
   `Fail` records failure and cancels queued work while preserving dispatched tasks.
   `Cancel` commits operation and queued-task cancellation with its event;
   dispatched tasks can still record results without starting new work.
4. Add public behavior tests inside the provider module, including concurrent writes,
   retries, and storage failures. Follow the shared [testing conventions](../framework/tests/README.md#writing-tests).

Run filesystem-provider checks from the repository root:

```sh
go test -race -count=1 ./providers/fs/...
go vet ./providers/fs/...
```

Run affected [framework and host tests](../framework/tests/README.md) when changing
shared behavior. Keep provider helpers inside that provider's module.

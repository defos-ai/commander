# Providers

## Overview

Providers implement the framework's messaging and persistence interfaces.
Application hosts select implementations by connection URL and inject them into
the runtime, keeping infrastructure details out of agents and controllers.

### Existing providers

| Provider | Implements | Connection formats |
| --- | --- | --- |
| [`fs`](fs/) | Message bus, artifacts, reports, inventory, executions, missions, operations, and action catalogs | Local paths, `~/path`, `file:///path`, `fs:///path` |

Only filesystem providers are implemented. RabbitMQ, S3, and MongoDB are planned;
unsupported URL schemes return an error.

### Filesystem behavior

The bus retains unacknowledged files for inspection, but does not automatically
redeliver them or provide a dead-letter queue. Subscriptions must be live when
messages are published. Shared filesystems must support file locks, atomic
renames, and hard links. The [message-handling guide](../framework/comms/README.md)
defines consumer acknowledgement policy.

Operations are stored in `DATABASE_URL/operations/<operation-id>.json`, each with
its own lock. Tasks, questions, and events remain in the operation snapshot so
related changes commit together. Reads use atomic snapshots without taking a write lock.
`OperationStore.GetTask` queries one task; `ListTasks` pages in assignment order
after a task ID, and `ListEvents` pages after an event sequence. Limits must be
positive. These filesystem queries currently read the operation snapshot.

## Quick start

For a private project, follow the [local module setup](../apps/agents/README.md#develop-an-agent-in-a-private-repository)
to add `commander/framework` and `commander/providers`. Within this repository,
[go.work](../go.work) already connects them. Import the factory you need:

| Factory | Host setting | Returns |
| --- | --- | --- |
| [`comms.OpenBus`](comms/factory.go) | `BUS_URL` | `comms.Bus` |
| [`artifact.OpenStorage`](artifact/factory.go) | `STORAGE_URL` | Handle with `ArtifactStore` |
| [`controller.OpenReportStorage`](controller/factory.go) | `STORAGE_URL` | Handle with `ReportStore` |
| [`controller.OpenDatabase`](controller/factory.go) | `DATABASE_URL` | Handle with domain stores |

For example, query missions through a filesystem database:

```go
package queries

import (
    "context"
    "errors"

    "commander/framework/domain/missions"
    provider "commander/providers/controller"
)

func ListMissions(ctx context.Context) (items []missions.Mission, err error) {
    database, err := provider.OpenDatabase(ctx, "./state/database")
    if err != nil {
        return nil, err
    }
    defer func() { err = errors.Join(err, database.Close()) }()
    return database.MissionStore.List(ctx)
}
```

Factories take URLs explicitly; hosts read environment variables and flags.
For agent artifact storage, pass `StorageOptions{AgentID: agentID}` using the
same ID as the agent. Stop runtime consumers before closing provider handles.
See [configuration](../docs/deployment.md#configuration) for connection settings.

## Development and testing

### Adding a backend

1. Add implementations under `providers/<backend>/`, following the `fs/` layout.
   Implement interfaces from `framework/comms`, `framework/artifact`, or
   `framework/domain`; providers must not depend on application runtimes.
2. Add URL selection to the relevant factory. Keep connection setup, credentials,
   and cleanup in the provider; return the existing framework interfaces.
3. Preserve the contracts for message acknowledgement, artifact ownership,
   immutable reports, execution outcomes, and atomic operation updates.
   `OperationStore.Update` applies a callback to the latest saved operation;
   locks and storage revisions stay inside the provider. Callbacks must be safe
   to retry and must not perform external side effects.
   `QueueTask` saves a direct assignment with its event and status update;
   retries with the same task ID preserve saved progress without another write.
   `DispatchTask` atomically claims an eligible queued task and saves its agent,
   timing, dispatch event, and operation status together, preserving its command ID.
   `FinishTask` saves the result, continuation, automatic follow-ups, and events
   together. Identical results return the saved state without rewriting it;
   conflicting outcomes cannot replace a final result.
   `ResolveContinuation` checks the decision version and saves its follow-up tasks
   and events atomically; a stale version cannot create a second branch.
   `Cancel` commits operation and queued-task cancellation with its event;
   dispatched tasks can still record results without starting new work.
4. Add public behavior tests under `providers/tests/`, including concurrent writes,
   retries, and storage failures. Follow the shared [testing conventions](../framework/tests/README.md#writing-tests).

From the repository root:

```sh
go test -race -count=1 ./providers/...
go vet ./providers/...
```

Run affected [framework and host tests](../framework/tests/README.md) when changing
shared behavior. Provider helpers belong in `providers/internal`.

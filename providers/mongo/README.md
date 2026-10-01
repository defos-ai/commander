# MongoDB provider

## Overview

`commander/providers/mongo` implements the database stores for missions, actions,
agent inventory, executions, operations, and accepted artifact metadata.
Controller and MCP factories select it for `mongodb://` or `mongodb+srv://`
`DATABASE_URL` values. Artifact bytes and reports use the separate `STORAGE_URL`.
Operation storage requires a replica set or sharded cluster for transactions.

## Quick start

From the repository root, start the local MongoDB service:

```sh
docker compose -f compose.full.yaml up --wait mongo
```

Compose initializes a single-node replica set and persists data in a named
volume. The host port defaults to `27017`; `MONGO_PORT` overrides it. For the
complete MongoDB + MinIO + FS bus stack, follow the
[deployment guide](../../docs/deployment.md).

URLs must include a database name. Use
`mongodb://127.0.0.1:27017/commander?replicaSet=rs0&directConnection=true` from the
host; Compose apps use `mongodb://mongo:27017/commander?replicaSet=rs0`.
`directConnection=true` lets host clients use the forwarded port even though
the replica-set member advertises its container hostname.

Apps share one [`MongoStore`](store/mongo_store.go) client across domain stores:

```go
import (
    mongomissions "commander/providers/mongo/missions"
    "commander/providers/mongo/store"
)

// ctx is the caller's context with a deadline.
database, err := store.Open(ctx,
    "mongodb://127.0.0.1:27017/commander?replicaSet=rs0&directConnection=true")
if err != nil {
    return err
}
// The host calls database.Close with a shutdown context after consumers stop.
missionStore := mongomissions.NewMissionMongoStore(database)
err = missionStore.Upsert(ctx, mission)
```

The repository workspace includes this module. Private projects use
[local module replacements](../../apps/agents/README.md#develop-an-agent-in-a-private-repository)
for `commander/framework` and `commander/providers/mongo`.

## Development and testing

With MongoDB running, run from the repository root:

```sh
COMMANDER_TEST_MONGO_URL='mongodb://127.0.0.1:27017/commander_mongo_tests?replicaSet=rs0&directConnection=true' \
  go test -race -count=1 -timeout=3m ./providers/mongo/...
go vet ./providers/mongo/...
```

Integration tests skip without `COMMANDER_TEST_MONGO_URL`. Use a dedicated test
database: tests clean their collections before and after each case. CI runs the
suite in [test-provider-mongo.yml](../../.github/workflows/test-provider-mongo.yml).
The same variable enables optional controller and MCP startup tests; see the
[testing guide](../../framework/tests/README.md).

### Persistence and recovery

Mission and action names are unique document IDs. Agent records contain advertised
action readiness and indexed heartbeat timestamps. Conditional writes reject equal
or older source observations. Expiry hides recent observations with a private
marker until they leave the heartbeat window; fresh observations reactivate the
record atomically. JSON schemas and operation
bodies preserve numeric precision; observation times retain sub-millisecond
precision alongside BSON dates. Executions atomically claim commands and keep
terminal outcomes immutable. The controller indexes immutable artifact metadata
after saving a successful result and before acknowledging acceptance. Result
replay and recovery repair incomplete metadata writes.

Operations use `operations`, `operation_tasks`, `operation_questions`, and
`operation_events` collections. A transaction commits metadata and changed
children together; unchanged children are not rewritten. A private parent
revision serializes changes to the same operation. Task order and event sequence
have indexed cursors. Transitions still read the full operation to apply shared
domain rules.

Keep collection names, document mapping, and domain errors in domain stores.
The shared `MongoStore` handles BSON queries, indexes, and transactions.
Transaction callbacks can retry: use the supplied context and keep external
effects outside the callback. Follow the
[`OperationStore` contract](../../framework/domain/operations/operation_store.go)
for reconciling uncertain writes before dispatching further work.

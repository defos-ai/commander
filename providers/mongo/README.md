# MongoDB provider

## Overview

`github.com/defos-ai/commander/providers/mongo` implements database stores for
missions, actions, agent inventory, controller registrations, executions,
operations, and accepted artifact metadata.
Controller and adapter factories select it for `mongodb://` or `mongodb+srv://`
`DATABASE_URL` values. Artifact bytes and reports use the separate `STORAGE_URL`.
Operation storage requires a replica set or sharded cluster for transactions.

## Quick start

From the repository root, start the local MongoDB service:

```sh
docker compose -f compose.full.yaml up --wait mongo
```

Compose initializes a single-node replica set and persists data in a named
volume. The host port defaults to `27017`; `MONGO_PORT` overrides it. For the
complete RabbitMQ + MongoDB + MinIO stack, follow the
[deployment guide](../../docs/deployment.md).

URLs must include a database name. Use
`mongodb://127.0.0.1:27017/commander?replicaSet=rs0&directConnection=true` from the
host; Compose apps use `mongodb://mongo:27017/commander?replicaSet=rs0`.
`directConnection=true` lets host clients use the forwarded port even though
the replica-set member advertises its container hostname.

Apps share one [`MongoStore`](store/mongo_store.go) client across domain stores:

```go
import (
    mongomissions "github.com/defos-ai/commander/providers/mongo/missions"
    "github.com/defos-ai/commander/providers/mongo/store"
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
for the framework and this provider.

## Development and testing

See [AGENTS.md](AGENTS.md) for transaction and persistence guidance.

With MongoDB running, run from the repository root:

```sh
COMMANDER_TEST_MONGO_URL='mongodb://127.0.0.1:27017/commander_mongo_tests?replicaSet=rs0&directConnection=true' \
  go test -race -count=1 -timeout=3m ./providers/mongo/...
go vet ./providers/mongo/...
```

Integration tests skip without `COMMANDER_TEST_MONGO_URL`. Use a dedicated test
database: tests clean their collections before and after each case. The same
variable enables the optional controller startup test. See the
[testing guide](../../framework/tests/README.md#mcp-api-tests) for full-stack tests.

## Persistence and recovery

Operations span metadata, task, question and event collections; transactions commit
related changes together. A standalone MongoDB server cannot support these writes.
See the [operation store](operations/) and
[`OperationStore` contract](../../framework/domain/operations/operation_store.go)
for persistence and uncertain-write recovery behavior.

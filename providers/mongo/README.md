# MongoDB provider

## Overview

`github.com/defos-ai/commander/providers/mongo` implements database stores for
missions, actions, agent inventory, controller registrations, executions,
operations, and accepted artifact metadata.
Controller and adapter factories select it for `mongodb://` or `mongodb+srv://`
`DATABASE_URL` values. Artifact bytes and reports use the separate `STORAGE_URL`.
Operation storage requires a replica set or sharded cluster for transactions.

## Quick start

From the repository root, start the full local stack:

```sh
docker compose -f compose.full.yaml up --build -d
```

Compose initializes a single-node replica set and persists data in a named
volume. MongoDB has no published host port; apps connect through Compose. See the
[deployment guide](../../docs/deployment.md) for the other services.

URLs must include a database name. Compose apps use
`mongodb://mongo:27017/commander?replicaSet=rs0`.

Apps share one [`MongoStore`](store/mongo_store.go) client across domain stores:

```go
import (
    mongomissions "github.com/defos-ai/commander/providers/mongo/missions"
    "github.com/defos-ai/commander/providers/mongo/store"
)

// ctx is the caller's context with a deadline.
database, err := store.Open(ctx,
    "mongodb://mongo:27017/commander?replicaSet=rs0")
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

Start the isolated test service and run from the repository root:

```sh
docker compose -f .github/composes/compose.providers.test.yaml up --wait mongo
COMMANDER_TEST_MONGO_URL='mongodb://127.0.0.1:27017/commander_mongo_tests?replicaSet=rs0&directConnection=true' \
  go test -race -count=1 -timeout=3m ./providers/mongo/...
go vet ./providers/mongo/...
docker compose -f .github/composes/compose.providers.test.yaml down --volumes
```

Integration tests skip without `COMMANDER_TEST_MONGO_URL`. Use a dedicated test
database: tests clean their collections before and after each case. The same
variable enables the optional controller startup test. See the
[testing guide](../../framework/tests/README.md#mcp-api-tests) for full-stack tests.
The test service exposes localhost port `27017`; `directConnection=true` handles
the replica set's container hostname. Always stop test services after failures too.

## Persistence and recovery

Operations span metadata, task, question and event collections; transactions commit
related changes together. A standalone MongoDB server cannot support these writes.
See the [operation store](operations/) and
[`OperationStore` contract](../../framework/domain/operations/operation_store.go)
for persistence and uncertain-write recovery behavior.

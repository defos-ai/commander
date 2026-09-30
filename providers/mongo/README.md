# Mongo provider

## Overview

An independent Go module implementing framework database stores with MongoDB.
Currently available: `MissionMongoStore`, `ActionMongoStore`, `AgentMongoStore`,
`ExecutionMongoStore`, `OperationMongoStore`, `ArtifactMongoStore`, and the shared `MongoStore` for
queries, conditional writes, indexes, and transactions. Controller and MCP hosts
select these stores when `DATABASE_URL` uses `mongodb://` or `mongodb+srv://`.
Operation updates require a replica set or sharded cluster for transactions.

`MongoStore` owns one client shared by injected domain stores. Mission and action
names are unique document IDs in separate collections. Fields use their JSON
names; JSON schemas remain verbatim BSON binary values so large numbers and
extension fields round-trip. Action catalogs accept only empty or `unknown`
status; readiness belongs to each agent. Agent inventory stores one document per
agent ID, including its advertised action readiness. A compound heartbeat index
supports atomic expiry checks. Observation timestamps retain sub-millisecond
precision alongside BSON dates, keeping ordering and expiry boundaries exact.

Executions atomically claim commands and preserve immutable terminal outcomes.
Accepted artifact metadata is immutable in the `artifacts` collection. The
controller indexes it after saving a validated successful execution, before
sending its receipt. Replaying or recovering that result repairs incomplete
metadata writes. File bytes use the separately configured `ArtifactFileStore`.

Operations use `operations`, `operation_tasks`, `operation_questions`, and
`operation_events` collections. Each transition commits metadata and changed
children in one transaction; unchanged child records are not rewritten. A private
parent revision serializes competing changes to the same operation. Task order
and event sequence have indexed cursors. JSON bodies preserve numeric precision
and timestamps, with searchable identity/order fields stored separately in BSON.
Transitions currently read the full operation to apply shared domain rules; this
is not yet a large-operation throughput optimization.

## Quick start

From the repository root, start Mongo:

```sh
docker compose -f compose.full.yaml up --wait mongo
```

For the complete local app stack, run
`docker compose -f compose.full.yaml up --build -d`. It uses Mongo for database
stores, MinIO for artifact files and reports, and a filesystem volume for the bus.
Compose initializes a single-node replica set and persists data in a named volume.
The host port defaults to `27017` (`MONGO_PORT` overrides it).

The repository workspace already includes this module. In a private project's
`go.mod`, use local replacements for now:

```go
require commander/providers/mongo v0.0.0

replace commander/providers/mongo => /path/to/commander/providers/mongo
replace commander/framework => /path/to/commander/framework
```

Run `go mod tidy` after adding imports. Public GitHub imports require the separate
module-path update. Mongo consumers need this module and the framework, without
the filesystem provider.

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
// The host closes database after all stores stop using it.
missionStore := mongomissions.NewMissionMongoStore(database)
err = missionStore.Upsert(ctx, mission)
```

URLs require a database name and support `mongodb://` and `mongodb+srv://`.
Use `mongo:27017` inside Compose; host connections use `directConnection=true`
because the replica-set member advertises its container hostname.

## Development and testing

Run the provider tests against the real Mongo service:

```sh
COMMANDER_TEST_MONGO_URL='mongodb://127.0.0.1:27017/commander_mongo_tests?replicaSet=rs0&directConnection=true' \
  go test -race -count=1 -timeout=3m ./providers/mongo/...
docker compose -f compose.full.yaml down --volumes
```

Database tests skip when `COMMANDER_TEST_MONGO_URL` is unset. Use a dedicated test
database: tests clean their collections before and after each case. CI runs the
same suite in [test-provider.yml](../../.github/workflows/test-provider.yml).
Optional controller and MCP startup tests use the same environment variable,
with separate `_controller_startup` and `_mcp_startup` databases. They create
indexes and verify startup/shutdown without deleting database contents.

Keep collection names, document mapping, and domain errors in each domain store.
The shared store exposes BSON queries and driver errors without interpreting
mission or operation state. Transaction callbacks may retry: use their supplied
context and keep external effects outside the callback. Store-specific error
mapping belongs to domain providers; callers reconcile uncertain writes before
dispatching further work.

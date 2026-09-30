# Mongo provider

## Overview

An independent Go module implementing framework database stores with MongoDB.
Currently available: `MissionMongoStore`, `ActionMongoStore`, `AgentMongoStore`,
and the shared `MongoStore` for queries, conditional writes, indexes, and
transactions. App factories select providers per dependency. Catalogs and agent
inventory support Mongo; operation and execution stores are not implemented yet,
so controller and MCP startup with a Mongo database URL remains blocked.

`MongoStore` owns one client shared by injected domain stores. Mission and action
names are unique document IDs in separate collections. Fields use their JSON
names; JSON schemas remain verbatim BSON binary values so large numbers and
extension fields round-trip. Action catalogs accept only empty or `unknown`
status; readiness belongs to each agent. Agent inventory stores one document per
agent ID, including its advertised action readiness. A compound heartbeat index
supports atomic expiry checks. Observation timestamps retain sub-millisecond
precision alongside BSON dates, keeping ordering and expiry boundaries exact.

## Quick start

From the repository root, start Mongo:

```sh
docker compose -f compose.full.yaml up --wait mongo
```

`compose.full.yaml` contains the full app layout with Mongo database URLs, staged
for the remaining provider work. **Only its Mongo service is ready in this step.**
It initializes a single-node replica set and persists data in a named volume.
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
    mongomissions "commander/providers/mongo/controller/missions"
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
same suite in [test-provider.yml](../../../.github/workflows/test-provider.yml).

Keep collection names, document mapping, and domain errors in each domain store.
The shared store exposes BSON queries and driver errors without interpreting
mission or operation state. Transaction callbacks may retry: use their supplied
context and keep external effects outside the callback. Operation-level handling
of uncertain writes is deferred until `OperationMongoStore` is implemented.

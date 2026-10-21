# MongoDB provider

## Overview

`github.com/defos-ai/commander/providers/mongo` implements database stores for
missions, actions, agent inventory, controller registrations, executions,
operations, encrypted secrets, and accepted artifact metadata.
Controller and adapter factories select it for `mongodb://` or `mongodb+srv://`
`DATABASE_URL` values. Artifact bytes and reports use the separate `STORAGE_URL`.
Operation and secret writes require a replica set or sharded cluster for transactions.

## Quick start

From the repository root, start the full local stack:

```sh
docker compose up --build -d
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

Use `Create` for new mission definitions; an existing name returns
`missions.ErrMissionExists` without changing its definition. `Upsert` replaces
definitions when seeding a catalog.

The repository workspace includes this module. Private projects use
[local module replacements](../../apps/agents/README.md#develop-an-agent-in-a-private-repository)
for the framework and this provider.

## Encrypted secrets

[`secrets.NewSecretMongoStore(ctx, database)`](secrets/secret_mongo_store.go) stores
encrypted records in the `secrets` collection. Encrypt values with
[`crypto.SecretCipher`](../../framework/crypto/README.md) before saving; the store
does not need the encryption key. Updates check the saved version, and deletion
retains the ID to reject delayed writes. Expired secrets remain available for
inventory. Use the [secret management service](../../framework/secrets/README.md)
or the authenticated [HTTP API](../../apps/adapters/api/README.md#secrets) to manage values.

Secrets are identified by agent type and name. Earlier development records and
indexes with profiles are incompatible; recreate those secrets in a fresh store.
Existing records are never merged or migrated automatically.

[`secrets.NewRequestMongoStore(ctx, database)`](secrets/request_mongo_store.go)
tracks pending requests, their immutable encrypted deliveries and installation
acknowledgements in `secret_requests`. It preserves terminal records for safe
retries; expiration is explicit. Deleting a primary secret does not erase these
delivery snapshots or copies already installed on agents. The store does not
publish deliveries or establish an agent's current readiness.

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

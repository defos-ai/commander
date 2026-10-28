# MongoDB provider

Retain the state and execution history of long-running multi-agent operations in
MongoDB. Operations, checkpoints, progress logs and encrypted secrets live
independently of running hosts; artifact files remain in the separate
`STORAGE_URL` backend.

## Quick start

[Compose](../../compose.yaml) initializes MongoDB automatically. For an existing
server, configure a URL with a database name:

```sh
DATABASE_URL='mongodb://mongo:27017/commander?replicaSet=rs0'
```

Use a replica set or sharded cluster; standalone servers cannot support the
required transactions.

To embed a store, share one client across domain stores:

```go
import (
    mongomissions "github.com/defos-ai/commander/providers/mongo/missions"
    "github.com/defos-ai/commander/providers/mongo/store"
)

database, err := store.Open(ctx, databaseURL)
if err != nil {
    return err
}
missionStore := mongomissions.NewMissionMongoStore(database)
```

Close the client after consumers stop. Use `Create` for new missions and `Upsert`
for seeded definitions. External projects can use the
[local module setup](../../apps/agents/README.md#develop-an-agent-in-a-private-repository).

## Encrypted secrets

Use [`NewSecretMongoStore`](secrets/secret_mongo_store.go) with the
[secret service](../../framework/secrets/README.md). Supply encryption keys to the
service, not MongoDB; back up the key separately from database records.

## Development and testing

Run from the repository root with a dedicated test database:

```sh
docker compose -f .github/composes/compose.providers.test.yaml up --wait mongo
COMMANDER_TEST_MONGO_URL='mongodb://127.0.0.1:27017/commander_mongo_tests?replicaSet=rs0&directConnection=true' \
  go test -race -count=1 -timeout=3m ./providers/mongo/...
go vet ./providers/mongo/...
docker compose -f .github/composes/compose.providers.test.yaml down --volumes
```

Tests skip without `COMMANDER_TEST_MONGO_URL` and clear their test collections.
Always stop the test stack, including after failures. See the
[testing guide](../../framework/tests/README.md#mcp-api-tests) for full-stack checks.

## Persistence and recovery

After an uncertain write, read saved state before retrying. Follow
[AGENTS.md](AGENTS.md) when changing transaction or recovery behavior.

Explore [10 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution and infrastructure
monitoring.

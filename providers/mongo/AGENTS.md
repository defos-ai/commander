# Working on the MongoDB provider

Inherit [provider guidance](../AGENTS.md). This module stores database records;
artifact bytes and reports belong to the separate storage provider.

## Client and persisted records

- Keep client creation/configuration in [`store.Open`](store/mongo_store.go).
  Require a database name and support `mongodb://` and `mongodb+srv://` through
  driver validation, including seed lists. Share one `MongoStore` across domain
  stores; the host closes it after consumers stop.
- Preserve primary reads, majority read/write concerns and snapshot transactions.
  Operation storage requires a replica set or sharded cluster; do not fall back
  to partially applied writes on standalone MongoDB.
- Keep MongoDB documents, BSON conversions and query/index details local. Use the
  existing document mappings where domain values need custom timestamp handling.
- Operations span metadata, task, question and event collections. Initialize
  collections/indexes before the first transaction; preserve unique positions and
  event keys within each operation.
- In operation mutations, reconstruct state from the latest transaction snapshot
  on every attempt, including generated child IDs and event sequences. Check the
  stored revision when replacing metadata; update related collections together.
- Keep [`operationWriteError`](operations/operation_mongo_store.go) conservative
  about ambiguous commits. Do not turn an unknown commit outcome into a definite
  rollback or discard `operations.ErrWriteUncertain` while wrapping errors.
- Test cleanup removes operation records and retires artifacts with private
  deletion markers. Preserve collections, indexes, catalogs and execution records;
  a delayed accepted-result write must not recreate deleted artifact metadata.

## Validation

Use a dedicated test database on a running replica set. Follow
[service setup](README.md#quick-start); tests clean their collections before and
after cases. Run from the repository root:

```sh
COMMANDER_TEST_MONGO_URL='mongodb://127.0.0.1:27017/commander_mongo_tests?replicaSet=rs0&directConnection=true' \
  go test -race -count=1 -timeout=3m ./providers/mongo/...
go vet ./providers/mongo/...
```

Integration tests skip unless `COMMANDER_TEST_MONGO_URL` is set. It also enables
the optional controller startup test; run that affected downstream check when
changing initialization. Preserve transaction rollback, concurrent mutation and
uncertain-write coverage when changing operation persistence. See the
[CI workflow](../../.github/workflows/test-provider-mongo.yml) and
[stack test guide](../../framework/tests/README.md) for broader checks.

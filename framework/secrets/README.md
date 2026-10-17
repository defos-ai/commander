# Secret management

`secrets.Service` creates, replaces and deletes named secrets through a
[`SecretStore`](../domain/secrets/secret_store.go). It encrypts values before
persistence and returns only metadata. Secrets belong to an agent type,
independently of operations.

```go
cipher, err := crypto.NewSecretCipher(encryptionKey)
if err != nil {
    return err
}
service, err := secrets.NewService(secretStore, cipher)
```

Use the [FS](../../providers/fs/README.md#encrypted-secrets) or
[Mongo](../../providers/mongo/README.md#encrypted-secrets) implementation. The
[HTTP API](../../apps/adapters/api/README.md#secrets) provides authenticated
management endpoints.

The service assigns identities, versions and timestamps. Replacement and deletion
require the current ID and version, so stale requests cannot overwrite a newer
value or delete a recreated secret. Optional expiration changes availability;
expired records remain in inventory.

An uncertain write may have succeeded. Read metadata before deciding what to do
next; the service never retries automatically. Attempted metadata returned with
`ErrWriteUncertain` is useful for reconciliation, not confirmation of persistence.

Hosts own authorization and [encryption key configuration](../crypto/README.md).
Saving a value does not deliver it to an agent or fulfill a pending secret request.

## Agent requirements

Each action declares its secret names through `Action.RequiredSecrets`, for example
`[]string{"GITHUB_TOKEN"}`. The agent's registered type identifies the scope.
Multiple actions may require the same name.

Declarations remain in agent snapshots after discovery succeeds. They identify
which secrets an agent uses; `MissingRequirements` separately reports what is
currently unavailable. Declarations do not grant access or change readiness, and
runtime distribution is not enabled yet.

The [synchronization contracts](../comms/messages/README.md#secret-synchronization-contracts)
define requests, encrypted deliveries and installation acknowledgements for the
next stage.

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

## Request and delivery tracking

[`RequestStore`](../domain/secrets/request_store.go) stores each pending request
with one immutable encrypted delivery and its installation acknowledgement.
Both FS and Mongo implementations preserve this record across restarts.

Save the request and delivery before publishing. Recovery reads `ListPending`,
then reuses the saved delivery ID, timestamps and ciphertext. A changed version
needs a fresh request; cancel the previous pending request first. Acknowledgements
must match the request, recipient, delivery, secret ID and version, and arrive
before the deadline. Exact replays preserve the original settlement time.
Cancellation and expiration are explicit; listing overdue requests does not
change them.

The store records installation history. It does not check sender authorization,
the agent's active request or the latest value in `SecretStore`. Runtime code must
check those before using the result for readiness. Requests remain independent:
one acknowledgement never fulfills another request for the same secret.

Terminal records retain their encrypted delivery snapshots. Deleting the primary
secret does not erase these copies or revoke values already installed by an agent.
The stores are available for integration; runtime distribution is not connected yet.

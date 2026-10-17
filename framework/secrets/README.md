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
Saving a value makes it available for asynchronous delivery; the API response
does not confirm installation.

## Agent requirements

Each action declares its secret names through `Action.RequiredSecrets`, for example
`[]string{"GITHUB_TOKEN"}`. The agent's registered type identifies the scope.
Multiple actions may require the same name.

Configure `AgentConfig.Secrets` with a decryptor using the same encryption key as
the API. Bundled agent hosts read optional `SECRETS_ENCRYPTION_KEY`; the controller
needs no key. It reads encrypted records from its configured database and sends
them only for names declared by the requesting agent's registered type.

Read a declared secret inside an action's discovery or run callback:

```go
value, err := action.Secret(ctx, "GITHUB_TOKEN")
if err != nil {
    return result, err
}
defer clear(value)
```

Values stay in agent memory and are not exported into the process environment.
Only the current action's declared names are accessible through its context.
Missing or expired secrets make the affected action unavailable; unrelated
actions remain usable. Execution checks availability again before calling the action.

By default, agents request fresh values every 30 seconds and retry outstanding
requests every 5 seconds. Installed values expire after at most one minute, or
earlier when the secret itself expires. Updates arrive on refresh; deletion or
loss of the controller blocks new use after the installed value's deadline.
This does not cancel actions already running or revoke copies they retained.

The bus and agent hosts form a trusted deployment boundary. Type/name checks
do not authenticate an untrusted bus publisher; restrict broker and filesystem
access. Cross-type requests, including a builder requesting another agent's
secrets, are not supported by this runtime policy.

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

Each refresh adds a retained request and, when available, an encrypted delivery
snapshot. Automatic history cleanup is not implemented. Deleting the primary
secret does not erase these copies or revoke values already installed by an agent.
The controller reconciles pending records on startup and periodically. Agents
keep values and active request identities in memory; a fresh agent process starts
with a new identity and requests its own values. See the
[wire protocol](../comms/messages/README.md#secret-synchronization-contracts).

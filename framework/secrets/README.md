# Secret management

Manage service credentials across agent workflows with encrypted storage,
action requirements and tracked delivery. Secrets belong to an agent type,
independently of operations. Users manage them
through [Admin UI](../../apps/admin/ui/README.md) or the authenticated
[HTTP API](../../apps/adapters/api/README.md#secrets); values stay out of chat.

For a custom host, construct the service with an encrypted
[FS](../../providers/fs/README.md#encrypted-secrets) or
[MongoDB](../../providers/mongo/README.md#encrypted-secrets) store:

```go
cipher, err := crypto.NewSecretCipher(encryptionKey)
if err != nil {
    return err
}
service, err := secrets.NewService(secretStore, cipher)
```

Use the current ID and version when replacing or deleting a value. After an
uncertain write, read metadata before retrying. Saving confirms storage, not
installation on an agent.

## Agent requirements

Configure `AgentConfig.Secrets` with the API's encryption key. Declare names in
`Action.RequiredSecrets` when they must be available before an action starts:

```go
value, err := action.Secret(ctx, "GITHUB_TOKEN")
if err != nil {
    return result, err
}
defer clear(value)
```

Missing secrets block only the affected actions. Values stay in memory and are
scoped to the agent's registered type; they are not environment variables.

Updates arrive on refresh, normally within 30 seconds. Installed copies expire
after at most one minute. Deletion does not cancel running actions or erase copies
they retained. Restrict broker and filesystem access to trusted hosts.

### Request a secret during execution

For a secret needed only for certain inputs, let the action check its environment
first, then request the managed value:

```go
value, err := action.RequestSecret(ctx, "GITHUB_TOKEN")
if err != nil {
    return result, err
}
defer clear(value)
```

Use this inside `Agent.Serve` with `AgentConfig.Secrets` configured. Set a context
deadline: the request waits for installation and does not pause the task timeout.
Omit the name from `RequiredSecrets` if the action can start without it.

Playground opens a masked form; Admin offers **Manage secrets**. Submit the value
there to resume waiting work after installation.

## Request and delivery tracking

Use [`RequestStore`](../domain/secrets/request_store.go) for durable delivery
history. Each refresh retains a request and, when available, an encrypted delivery.
Deleting a primary secret does not erase that history; account for it when
planning retention and backups.

For exchange and persistence changes, follow [AGENTS.md](AGENTS.md) and the
[wire guidance](../comms/messages/AGENTS.md).

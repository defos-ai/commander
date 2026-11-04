# Secret management

Build encrypted credential storage and delivery for agents working across
business systems. One name identifies one shared value across the deployment.
For providing and rotating values, follow the
[shared secrets guide](../../docs/features/secrets.md).

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

Set `AgentConfig.Secrets` to `&agent.SecretConfig{}`; agents do not need an
encryption key. Declare names in
`Action.RequiredSecrets` when they must be available before an action starts:

```go
value, err := action.Secret(ctx, "GITHUB_TOKEN")
if err != nil {
    return result, err
}
defer clear(value)
```

Missing secrets block only the affected actions. Each agent tracks its own
delivery and installation; values stay in memory rather than process environment
variables.

Default settings refresh declared values every 30 seconds with one-minute leases.
Deletion does not cancel running actions or erase copies they retained. The
controller decrypts requested values for delivery; database records and delivery
history stay encrypted. Restrict broker and filesystem access to trusted hosts,
and use AMQPS for network secret delivery. Retain the host's
[encryption key](../../apps/adapters/api/README.md#secrets) across restarts.

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

For a reusable value, the agent can supply a default from its code, environment
or memory:

```go
value, err := action.RequestReusableSecret(ctx, "LOGIN_FOR_CRM",
    action.SecretRequestOptions{Default: []byte("admin")},
)
if err != nil {
    return result, err
}
defer clear(value)
```

An existing saved value takes precedence. If missing, the controller encrypts and
saves the default as a shared secret, so other agents can request it without a
default. Defaults never replace or renew existing values, including expired ones.
Keep the name out of `RequiredSecrets` so the action can reach this call.

## Request and delivery tracking

Use [`RequestStore`](../domain/secrets/request_store.go) for durable delivery
history. Each refresh retains a request and, when available, an encrypted delivery.
Deleting a primary secret does not erase that history; account for it when
planning retention and backups.

For exchange and persistence changes, follow [AGENTS.md](AGENTS.md) and the
[wire guidance](../comms/messages/AGENTS.md).

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.

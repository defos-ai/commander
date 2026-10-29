# Cryptography

Protect shared integration credentials used by distributed agent workflows.
Use `crypto.SecretCipher` to encrypt stored deployment secrets with AES-256-GCM.
Values and their metadata are authenticated together so modified records fail
authentication.
For versioned create/update/delete operations, use the
[secret management service](../secrets/README.md).

```go
cipher, err := crypto.NewSecretCipher(os.Getenv("SECRETS_ENCRYPTION_KEY"))
if err != nil {
    return err
}
record, err := cipher.Encrypt(metadata, value)
```

Save `record` through `SecretStore`. Use `cipher.Decrypt(record)` only after
checking access, the expected version and expiry.

## Key lifecycle

- Supply a random 32-byte key encoded as 64 hex characters. Compose and all-in-one
  configure it for you; [custom deployments](../../docs/deployment.md) must supply it.
- Retain the same key across restarts and back it up separately from encrypted data.
  Losing it makes stored secrets unreadable.
- Share it only with trusted encrypting/decrypting hosts. The controller and stores
  do not need it.
- Do not replace it as a rotation shortcut: existing records need re-encryption.
- Replace it through a planned rotation before encrypting 2^32 records with one key.

See [AGENTS.md](AGENTS.md) for cryptographic implementation constraints.

Explore [11 practical Commander use cases](https://github.com/defos-ai/commander/blob/main/docs/use-cases.md):
security operations, authorized security testing, compliance monitoring,
multi-agent development, incident response, prospect research and outreach,
competitive intelligence, data processing, ticket resolution, infrastructure
monitoring and long-running research for universities and R&D centers.

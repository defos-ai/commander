# Cryptography

`crypto.SecretCipher` encrypts secret values using AES-256-GCM and authenticates their
identity, scope, version and timestamps. It uses Go's
[random-nonce implementation](https://pkg.go.dev/crypto/cipher#NewGCMWithRandomNonce).

Applications pass the same `SECRET_ENCRYPTION_KEY` to `crypto.NewSecretCipher` in every
trusted process that encrypts or decrypts secrets. Use a randomly generated
32-byte key encoded as 64 hex characters, and retain it across restarts.
Missing or malformed keys return an error; no replacement key is generated.

```go
cipher, err := crypto.NewSecretCipher(os.Getenv("SECRET_ENCRYPTION_KEY"))
if err != nil {
    return err
}
record, err := cipher.Encrypt(metadata, value)
```

Persist `record` through `secrets.SecretStore`; use `record.Metadata` for public
inventories. `cipher.Decrypt(record)` returns the original bytes after authentication.
Applications must separately check access, the current secret version and expiry.

The envelope format is `aes-256-gcm-v2`; unsupported formats are rejected.

The [secret management service](../secrets/README.md) handles versioned writes.
The [HTTP API](../../apps/adapters/api/README.md#secrets) reads this environment
variable to enable secret management. Agent delivery is separate.

## Key lifecycle

Keep the key separate from stored records and backups of those records. Losing it
makes the encrypted values unreadable. Changing it does not re-encrypt existing
records; rotation needs an explicit re-encryption procedure. Everyone holding
the same key can decrypt any matching ciphertext they can access.

One key must encrypt fewer than 2^32 records across all processes and restarts.
That limit comes from the random nonce design; this package cannot count uses
across processes. Hosts must manage the key's lifetime.

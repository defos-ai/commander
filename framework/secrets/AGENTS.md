# Working on secret management

Inherit [framework guidance](../AGENTS.md).

- [`Service`](service.go) owns secret mutation policy: new UUID/version/timestamps
  on create, immutable ID/key/creation time on replace, and caller-supplied ID plus
  version for replace/delete. Keep final atomic conflict checks in `SecretStore`.
- Inject the store and `Encryptor`; never import concrete providers, read the
  environment, generate fallback encryption keys, or decrypt stored values here.
  Encrypt before any persistence call. Return metadata only, including inventory.
- Use `domain.ErrInvalidInput` for caller validation failures. Preserve store
  conflict/not-found/uncertain errors. Never retry writes or turn uncertain outcomes
  into success. Attempted metadata accompanying uncertainty is not a receipt.
- Secret availability is distinct from request fulfillment and agent readiness.
  This service does not dispatch work or settle pending requests.
- Authorization belongs to hosts. Keep errors free of secret values and clear
  temporary plaintext copies when no longer needed; do not promise complete Go
  memory erasure.
- Tests belong in [`../tests/secrets`](../tests/secrets/). Cover encryption before
  persistence, immutable identity, stale updates, expiry, and failure/uncertainty
  without logging values or keys. Follow the framework race/vet checks.

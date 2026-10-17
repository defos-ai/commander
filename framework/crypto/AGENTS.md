# Working on cryptography

- [`SecretCipher`](secret_cipher.go) owns key parsing and AES-256-GCM record encryption.
  Applications load `SECRETS_ENCRYPTION_KEY`; never read it or generate a fallback
  in this package. Keep malformed key errors free of supplied values.
- Use `crypto/cipher.NewGCMWithRandomNonce`. It prepends a 12-byte nonce; split
  this into the domain envelope on encryption and reconstruct it on decryption.
  Reject bad nonce/tag lengths before calling the standard library. Do not offer
  caller-selected nonces or a plaintext fallback.
- [`secret_binding.go`](secret_binding.go) fixes v2 authenticated data independently of domain
  struct layout. Bind all metadata, format and key ID. Preserve UTC nanosecond
  timestamp precision across JSON and provider serialization; BSON dates alone
  lose precision. A format change must be explicit.
- Encryption authenticates bytes, not authorization, freshness or installation.
  Runtime/service code must check expected ID/version and expiry, and delivery
  must additionally bind the recipient and request. Do not reuse a stored envelope
  as proof that a recipient was authorized to receive it.
- Key IDs are nonsecret fingerprints. Keep cipher fields private and diagnostic
  formatting redacted. This does not promise erasure of every key/plaintext copy
  in Go memory. Caller-owned inputs must not be mutated or retained.
- Tests live in [`../tests/secrets`](../tests/secrets/). Run the framework race
  suite and vet from the parent guide. Cover metadata/ciphertext tampering,
  malformed envelopes, wrong keys, serialization and concurrent use without
  logging key material or plaintext fixtures on failure.

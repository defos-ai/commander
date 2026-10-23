// Package crypto provides cryptographic components for Commander. SecretCipher
// encrypts secret records; applications supply keys and enforce access and freshness.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/defos-ai/commander/framework/domain/secrets"
)

const (
	Format = "aes-256-gcm-v2"

	keySize             = 32
	nonceSize           = 12
	tagSize             = 16
	maxGCMPlaintextSize = (1 << 36) - 32
)

var (
	ErrMissingKey = errors.New("secret encryption key is required")
	ErrInvalidKey = errors.New("secret encryption key must contain 64 hexadecimal characters (32 bytes)")
	ErrDecrypt    = errors.New("cannot authenticate encrypted secret")
)

// SecretCipher uses one configured key. It is safe for concurrent use; construct it
// with NewSecretCipher. The same key must not encrypt more than 2^32 records across all
// processes and restarts, as required by cipher.NewGCMWithRandomNonce.
type SecretCipher struct {
	aead  cipher.AEAD
	keyID string
}

// NewSecretCipher accepts an explicitly configured 256-bit key encoded as hex. It never
// generates a replacement key or reads the environment. Hosts choose when the
// feature is enabled and supply SECRETS_ENCRYPTION_KEY; keep that key across
// restarts and use the same value in every trusted encrypting/decrypting process.
func NewSecretCipher(encodedKey string) (*SecretCipher, error) {
	if encodedKey == "" {
		return nil, ErrMissingKey
	}
	if len(encodedKey) != keySize*2 {
		return nil, ErrInvalidKey
	}
	key, err := hex.DecodeString(encodedKey)
	defer clear(key)
	if err != nil {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("cannot initialize secret encryption cipher")
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, errors.New("cannot initialize secret authenticated encryption")
	}
	// A public fingerprint identifies the configured random key, never its value.
	// Truncation keeps it within the domain's identifier length limit.
	fingerprint := sha256.Sum256(key)
	return &SecretCipher{aead: aead, keyID: "sha256-" + hex.EncodeToString(fingerprint[:16])}, nil
}

// Validate lets hosts reject an uninitialized cipher before starting providers.
func (c *SecretCipher) Validate() error {
	if c == nil || c.aead == nil {
		return ErrMissingKey
	}
	return nil
}

// String and GoString prevent ordinary diagnostic formatting from traversing
// the initialized cipher's private key material. There are no public JSON fields.
func (SecretCipher) String() string   { return "secret cipher (key redacted)" }
func (SecretCipher) GoString() string { return "secret cipher (key redacted)" }

// Encrypt authenticates every metadata field along with the ciphertext. The
// returned record owns its bytes; it does not retain or modify plaintext.
// Empty values are valid cryptographically; requirement validation belongs to
// the consuming application. Encrypt does not decide availability or access.
func (c *SecretCipher) Encrypt(metadata secrets.Metadata, plaintext []byte) (secrets.Secret, error) {
	if c == nil || c.aead == nil {
		return secrets.Secret{}, ErrMissingKey
	}
	if err := metadata.Validate(); err != nil {
		return secrets.Secret{}, fmt.Errorf("encrypt secret metadata: %w", err)
	}
	if uint64(len(plaintext)) > maxGCMPlaintextSize {
		return secrets.Secret{}, errors.New("secret exceeds the encryption format's size limit")
	}
	sealed := c.aead.Seal(nil, nil, plaintext, authenticatedData(metadata, Format, c.keyID))
	// The standard library generates and prepends the nonce. Keep the domain's
	// separate nonce field without accepting caller-supplied nonces for encryption.
	return secrets.Secret{
		Metadata: metadata,
		Value: secrets.EncryptedValue{
			Format: Format, KeyID: c.keyID,
			Nonce: slices.Clone(sealed[:nonceSize]), Ciphertext: sealed[nonceSize:],
		},
	}, nil
}

// Decrypt authenticates a record and returns independent plaintext bytes. Every
// malformed, unsupported or unauthenticated record returns ErrDecrypt without
// plaintext. Callers must check the expected ID/version, availability and access:
// an authentic older or expired record can still be decrypted by this primitive.
func (c *SecretCipher) Decrypt(secret secrets.Secret) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrMissingKey
	}
	if err := secret.Validate(); err != nil {
		return nil, ErrDecrypt
	}
	value := secret.Value
	if value.Format != Format || value.KeyID != c.keyID || len(value.Nonce) != nonceSize ||
		len(value.Ciphertext) < tagSize || uint64(len(value.Ciphertext)) > maxGCMPlaintextSize+tagSize {
		return nil, ErrDecrypt
	}
	sealed := make([]byte, nonceSize+len(value.Ciphertext))
	copy(sealed, value.Nonce)
	copy(sealed[nonceSize:], value.Ciphertext)
	plaintext, err := c.aead.Open(nil, nil, sealed, authenticatedData(secret.Metadata, value.Format, value.KeyID))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

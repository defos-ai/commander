package secrets

import (
	"errors"
	"slices"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

type Availability string

const (
	Missing   Availability = "missing"
	Available Availability = "available"
	Expired   Availability = "expired"
)

// Metadata is safe to include in inventories. It contains neither plaintext nor
// ciphertext. Version changes when the value or its validity changes; it is not
// a provider storage revision. Recreating a deleted key requires a new ID.
type Metadata struct {
	ID        string    `json:"id"`
	Key       Key       `json:"key"`
	Version   uint64    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

func (value Metadata) Validate() error {
	if !id.Valid(value.ID) {
		return errors.New("secret ID must be a canonical UUID")
	}
	if err := value.Key.Validate(); err != nil {
		return err
	}
	if value.Version == 0 {
		return errors.New("secret version must be positive")
	}
	if !validTime(value.CreatedAt) || !validTime(value.UpdatedAt) || value.UpdatedAt.Before(value.CreatedAt) {
		return errors.New("secret timestamps must be valid and update must not precede creation")
	}
	if !value.ExpiresAt.IsZero() && (!validTime(value.ExpiresAt) || !value.ExpiresAt.After(value.UpdatedAt)) {
		return errors.New("secret expiration must follow its update time")
	}
	return nil
}

func AvailabilityAt(value *Metadata, now time.Time) Availability {
	if value == nil {
		return Missing
	}
	if !value.ExpiresAt.IsZero() && !now.Before(value.ExpiresAt) {
		return Expired
	}
	return Available
}

// EncryptedValue is an opaque encryption envelope. Validate checks structure
// only: the encryption service must authenticate the ciphertext and its binding
// to secret identity, scope, version and validity before using any plaintext.
// Format identifies the encryption service's envelope format, not a secret version.
type EncryptedValue struct {
	Format     string `json:"format"`
	KeyID      string `json:"key_id"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func (value EncryptedValue) Validate() error {
	if !id.ValidType(value.Format) || !id.ValidType(value.KeyID) {
		return errors.New("encrypted secret requires a valid format and key identifier")
	}
	if len(value.Nonce) == 0 || len(value.Ciphertext) == 0 {
		return errors.New("encrypted secret requires a nonce and ciphertext")
	}
	return nil
}

func (value EncryptedValue) Clone() EncryptedValue {
	value.Nonce = slices.Clone(value.Nonce)
	value.Ciphertext = slices.Clone(value.Ciphertext)
	return value
}

// Secret is the storage record, not a public API response. Encrypt before
// constructing it; use Metadata for controller, discovery and UI snapshots.
type Secret struct {
	Metadata Metadata       `json:"metadata"`
	Value    EncryptedValue `json:"value"`
}

func (secret Secret) Validate() error {
	if err := secret.Metadata.Validate(); err != nil {
		return err
	}
	return secret.Value.Validate()
}

func (secret Secret) Clone() Secret {
	secret.Value = secret.Value.Clone()
	return secret
}

func validTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}

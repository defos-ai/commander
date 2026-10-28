package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

// Retirement drops ciphertext and retains only identity and name. Keep markers
// indefinitely so delayed writes cannot recreate old identities after a restart.
type retiredSecret struct {
	ID  string          `json:"id"`
	Key secretmodel.Key `json:"key"`
}

type storedRecord struct {
	Secret  *secretmodel.Secret `json:"secret,omitempty"`
	Retired *retiredSecret      `json:"retired,omitempty"`
}

func (record storedRecord) identity() retiredSecret {
	if record.Secret != nil {
		return retiredSecret{ID: record.Secret.Metadata.ID, Key: record.Secret.Metadata.Key}
	}
	return *record.Retired
}

func decode(data []byte) ([]storedRecord, error) {
	var records []storedRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		// JSON errors can echo arbitrary stored strings; never include their values.
		return nil, errors.New("invalid secret collection encoding")
	}
	if err := decoder.Decode(new(any)); err != io.EOF || records == nil {
		return nil, errors.New("secret collection must contain one JSON array")
	}
	identities := make(map[string]bool, len(records))
	activeKeys := make(map[secretmodel.Key]bool, len(records))
	for _, record := range records {
		if (record.Secret == nil) == (record.Retired == nil) {
			return nil, errors.New("secret collection entry must contain a secret or a retired identity")
		}
		if record.Secret != nil {
			if err := record.Secret.Validate(); err != nil {
				return nil, err
			}
			key := record.Secret.Metadata.Key
			if activeKeys[key] {
				return nil, errors.New("secret collection contains duplicate active keys")
			}
			activeKeys[key] = true
		}
		identity := record.identity()
		if !id.Valid(identity.ID) {
			return nil, errors.New("stored secret identity must be a canonical UUID")
		}
		if err := identity.Key.Validate(); err != nil {
			return nil, err
		}
		if identities[identity.ID] {
			return nil, errors.New("secret collection contains duplicate identities")
		}
		identities[identity.ID] = true
	}
	return records, nil
}

func findActive(records []storedRecord, key secretmodel.Key) int {
	for index, record := range records {
		if record.Secret != nil && record.Secret.Metadata.Key == key {
			return index
		}
	}
	return -1
}

func equalSecrets(left, right secretmodel.Secret) bool {
	a, b := left.Metadata, right.Metadata
	return a.ID == b.ID && a.Key == b.Key && a.Version == b.Version &&
		a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt) && a.ExpiresAt.Equal(b.ExpiresAt) &&
		left.Value.Format == right.Value.Format && left.Value.KeyID == right.Value.KeyID &&
		bytes.Equal(left.Value.Nonce, right.Value.Nonce) && bytes.Equal(left.Value.Ciphertext, right.Value.Ciphertext)
}

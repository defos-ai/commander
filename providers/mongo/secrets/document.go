package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

const collection = "secrets"

// JSON preserves authenticated timestamp nanoseconds and the full uint64 version
// range. Indexed fields are checked against that payload on every read. Retired
// documents retain only identity and scope, never the encrypted value.
type secretDocument struct {
	ID        string          `bson:"_id"`
	AgentType string          `bson:"agent_type"`
	Profile   string          `bson:"profile"`
	Name      string          `bson:"name"`
	Active    bool            `bson:"active"`
	Data      json.RawMessage `bson:"data,omitempty"`
}

func identityDocument(secretID string, key secretmodel.Key) secretDocument {
	return secretDocument{ID: secretID, AgentType: key.Scope.AgentType, Profile: key.Scope.Profile, Name: key.Name}
}

func encode(secret secretmodel.Secret) (secretDocument, error) {
	if err := secret.Validate(); err != nil {
		return secretDocument{}, err
	}
	doc := identityDocument(secret.Metadata.ID, secret.Metadata.Key)
	doc.Active = true
	var err error
	doc.Data, err = json.Marshal(secret)
	return doc, err
}

func (d secretDocument) key() secretmodel.Key {
	return secretmodel.Key{Scope: secretmodel.Scope{AgentType: d.AgentType, Profile: d.Profile}, Name: d.Name}
}

func (d secretDocument) decode() (secretmodel.Secret, error) {
	if !id.Valid(d.ID) {
		return secretmodel.Secret{}, errors.New("stored secret ID must be a canonical UUID")
	}
	if err := d.key().Validate(); err != nil {
		return secretmodel.Secret{}, err
	}
	if !d.Active {
		if len(d.Data) != 0 {
			return secretmodel.Secret{}, errors.New("retired secret must not contain a value")
		}
		return secretmodel.Secret{}, nil
	}
	var secret secretmodel.Secret
	decoder := json.NewDecoder(bytes.NewReader(d.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&secret); err != nil {
		// Decoding errors may include stored strings; do not expose their values.
		return secretmodel.Secret{}, errors.New("invalid stored secret encoding")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return secretmodel.Secret{}, errors.New("stored secret must contain one JSON value")
	}
	if err := secret.Validate(); err != nil {
		return secretmodel.Secret{}, err
	}
	if secret.Metadata.ID != d.ID || secret.Metadata.Key != d.key() {
		return secretmodel.Secret{}, errors.New("stored secret identity does not match its indexed fields")
	}
	return secret, nil
}

func equalSecrets(left, right secretmodel.Secret) bool {
	a, b := left.Metadata, right.Metadata
	return a.ID == b.ID && a.Key == b.Key && a.Version == b.Version &&
		a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt) && a.ExpiresAt.Equal(b.ExpiresAt) &&
		left.Value.Format == right.Value.Format && left.Value.KeyID == right.Value.KeyID &&
		bytes.Equal(left.Value.Nonce, right.Value.Nonce) && bytes.Equal(left.Value.Ciphertext, right.Value.Ciphertext)
}

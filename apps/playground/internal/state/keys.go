package state

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

const cacheKeyName = "PLAYGROUND_PROVIDER_KEYS"

type keyCache struct {
	Version int               `json:"version"`
	Values  map[string]string `json:"values"`
}

func (p *Profile) LoadKey(providerID, baseURL string) (string, error) {
	if providerID == "" || baseURL == "" {
		return "", errors.New("provider and base URL are required to load a key")
	}
	if err := p.checkMarker(); err != nil {
		return "", err
	}
	values, err := p.loadKeys()
	if err != nil {
		return "", err
	}
	return values[keyIdentity(providerID, baseURL)], nil
}

func (p *Profile) SaveKey(providerID, baseURL, value string) error {
	if providerID == "" || baseURL == "" || value == "" {
		return errors.New("provider, base URL and nonempty value are required to save a key")
	}
	if err := p.checkMarker(); err != nil {
		return err
	}
	values, err := p.loadKeys()
	if err != nil {
		return err
	}
	cipher, err := p.keyCipher(true)
	if err != nil {
		return err
	}
	values[keyIdentity(providerID, baseURL)] = value
	data, err := json.Marshal(keyCache{Version: 1, Values: values})
	if err != nil {
		return err
	}
	defer clear(data)
	now := time.Now().UTC()
	record, err := cipher.Encrypt(secrets.Metadata{
		ID: id.New(), Key: secrets.Key{Name: cacheKeyName}, Version: 1, CreatedAt: now, UpdatedAt: now,
	}, data)
	if err != nil {
		return errors.New("could not encrypt saved provider keys")
	}
	return writeJSON(p.root, "provider-keys.json", record)
}

func (p *Profile) loadKeys() (map[string]string, error) {
	data, err := readFile(p.root, "provider-keys.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	cipher, cipherErr := p.keyCipher(false)
	if cipherErr != nil {
		return nil, cipherErr
	}
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if cipher == nil {
		return nil, errors.New("saved provider keys exist without their local encryption key; refusing to replace it")
	}
	var record secrets.Secret
	if decodeJSON(data, &record) != nil || record.Validate() != nil || record.Metadata.Key.Name != cacheKeyName || record.Metadata.Version != 1 || !record.Metadata.ExpiresAt.IsZero() {
		return nil, errors.New("saved provider key record is invalid")
	}
	plaintext, err := cipher.Decrypt(record)
	if err != nil {
		return nil, errors.New("saved provider keys could not be authenticated")
	}
	defer clear(plaintext)
	var cache keyCache
	if decodeJSON(plaintext, &cache) != nil || cache.Version != 1 || cache.Values == nil {
		return nil, errors.New("saved provider key cache is invalid or incompatible")
	}
	for name, value := range cache.Values {
		if len(name) != sha256.Size*2 || value == "" {
			return nil, errors.New("saved provider key entry is invalid")
		}
		if _, err := hex.DecodeString(name); err != nil {
			return nil, errors.New("saved provider key entry is invalid")
		}
	}
	return cache.Values, nil
}

func (p *Profile) keyCipher(create bool) (*crypto.SecretCipher, error) {
	key, err := readFile(p.root, "provider-key.key")
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil, nil
		}
		if _, err := p.root.Lstat("provider-keys.json"); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("saved provider keys exist without their local encryption key; refusing to replace it")
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		defer clear(secret)
		key = []byte(hex.EncodeToString(secret))
		if err := writeFile(p.root, "provider-key.key", key); err != nil {
			clear(key)
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	defer clear(key)
	cipher, err := crypto.NewSecretCipher(string(key))
	if err != nil {
		return nil, errors.New("local provider-key encryption key is invalid; refusing to replace it")
	}
	return cipher, nil
}

func keyIdentity(providerID, baseURL string) string {
	encoded, _ := json.Marshal([2]string{providerID, baseURL})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

package local

import (
	"fmt"
	"os"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/host"
)

func secretCipher() (*crypto.SecretCipher, error) {
	key := os.Getenv("SECRETS_ENCRYPTION_KEY")
	if key == "" {
		// The generated 32-byte hex key has the same lifetime as session state.
		var err error
		key, err = host.GeneratePassword()
		if err != nil {
			return nil, fmt.Errorf("generate secrets encryption key: %w", err)
		}
	}
	cipher, err := crypto.NewSecretCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SECRETS_ENCRYPTION_KEY: %w", err)
	}
	return cipher, nil
}

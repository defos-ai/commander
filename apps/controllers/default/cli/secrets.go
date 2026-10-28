package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/providers/fs"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
	mongosecrets "github.com/defos-ai/commander/providers/mongo/secrets"
)

func secretDecryptor(cipher *crypto.SecretCipher) (consumers.SecretDecryptor, error) {
	if cipher != nil {
		if err := cipher.Validate(); err != nil {
			return nil, fmt.Errorf("secret cipher: %w", err)
		}
		return cipher, nil
	}
	key := os.Getenv("SECRETS_ENCRYPTION_KEY")
	if key == "" {
		return nil, nil
	}
	cipher, err := crypto.NewSecretCipher(key)
	if err != nil {
		return nil, fmt.Errorf("SECRETS_ENCRYPTION_KEY: %w", err)
	}
	return cipher, nil
}

func (d *dependencies) createSecretStores(ctx context.Context, rawURL string) (secrets.SecretStore, secrets.RequestStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	switch scheme := connectionScheme(rawURL); scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, nil, err
		}
		values, err := fssecrets.NewSecretFsStore(path)
		if err != nil {
			return nil, nil, err
		}
		requests, err := fssecrets.NewRequestFsStore(path)
		return values, requests, err
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, nil, err
		}
		values, err := mongosecrets.NewSecretMongoStore(ctx, d.mongo)
		if err != nil {
			return nil, nil, err
		}
		requests, err := mongosecrets.NewRequestMongoStore(ctx, d.mongo)
		return values, requests, err
	default:
		return nil, nil, fmt.Errorf("unsupported secret database scheme %q", scheme)
	}
}

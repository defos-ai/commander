package cli

import (
	"context"
	"fmt"

	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/providers/fs"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
	mongosecrets "github.com/defos-ai/commander/providers/mongo/secrets"
)

func (d *dependencies) createSecretStore(ctx context.Context, rawURL string) (secrets.SecretStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch scheme := connectionScheme(rawURL); scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fssecrets.NewSecretFsStore(path)
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, err
		}
		return mongosecrets.NewSecretMongoStore(ctx, d.mongo)
	default:
		return nil, fmt.Errorf("unsupported secret store scheme %q", scheme)
	}
}

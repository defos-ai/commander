package consumers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
	secretservice "github.com/defos-ai/commander/framework/secrets"
)

func (c *SecretEventConsumer) storeDefault(ctx context.Context, request secrets.Request, value []byte) error {
	ctx, cancel := context.WithDeadline(ctx, request.ExpiresAt)
	defer cancel()
	_, err := c.Secrets.GetMetadata(ctx, request.Key)
	if err == nil {
		return nil
	}
	if !errors.Is(err, secrets.ErrUnknownSecret) || errors.Is(err, secrets.ErrWriteUncertain) {
		return secretDefaultError(err)
	}
	encryptor, ok := c.Decryptor.(secretservice.Encryptor)
	if !ok {
		return errors.New("secret default requires an encryptor")
	}
	service, err := secretservice.NewService(c.Secrets, encryptor)
	if err != nil {
		return secretDefaultError(err)
	}
	_, err = service.Create(ctx, request.Key, value, time.Time{})
	if errors.Is(err, secrets.ErrSecretConflict) && !errors.Is(err, secrets.ErrWriteUncertain) {
		_, err = c.Secrets.GetMetadata(ctx, request.Key)
	}
	return secretDefaultError(err)
}

func secretDefaultError(err error) error {
	if err == nil {
		return nil
	}
	for _, kind := range []error{secrets.ErrWriteUncertain, context.Canceled, context.DeadlineExceeded, secrets.ErrUnknownSecret, secrets.ErrSecretConflict} {
		if errors.Is(err, kind) {
			return fmt.Errorf("could not store secret default: %w", kind)
		}
	}
	return errors.New("could not store secret default")
}

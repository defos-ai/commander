package controller

import (
	"context"

	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

func (c *Controller) ProvideTransient(ctx context.Context, requestID string, value []byte) error {
	_, err := request(ctx, c, func(ctx context.Context) (struct{}, error) {
		if c.secrets == nil {
			return struct{}{}, secrets.ErrUnavailable
		}
		if err := (controllercommands.ProvideTransientSecretCommand{RequestID: requestID, Value: value}).Validate(); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, c.secrets.ProvideTransient(ctx, requestID, value)
	})
	return err
}

var _ controllercommands.SecretRequests = (*Controller)(nil)

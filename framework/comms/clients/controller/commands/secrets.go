package commands

import (
	"bytes"
	"context"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

type ProvideTransientSecretCommand struct {
	RequestID string `json:"request_id"`
	Value     []byte `json:"value"`
}

func (command ProvideTransientSecretCommand) Validate() error {
	if !id.Valid(command.RequestID) || command.Value == nil || len(command.Value) > secrets.MaxTransientSecretBytes {
		return domain.ErrInvalidInput
	}
	return nil
}

func (ProvideTransientSecretCommand) String() string {
	return "transient secret submission (value redacted)"
}
func (ProvideTransientSecretCommand) GoString() string {
	return "transient secret submission (value redacted)"
}

func (c *Client) ProvideTransient(ctx context.Context, requestID string, value []byte) error {
	command := ProvideTransientSecretCommand{RequestID: requestID, Value: bytes.Clone(value)}
	defer clear(command.Value)
	return c.call(ctx, MethodProvideTransientSecret, command, &struct{}{})
}

var _ SecretRequests = (*Client)(nil)

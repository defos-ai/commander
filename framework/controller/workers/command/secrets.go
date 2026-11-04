package command

import (
	"context"
	"encoding/json"
	"errors"

	adapterevents "github.com/defos-ai/commander/framework/comms/clients/adapter/events"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

// The request ledger retains command payloads for replay. Transient values must
// bypass it; the secret request and its held delivery already provide identity.
func (c *Worker) consumeTransient(ctx context.Context, message messages.Message, envelope controllercommands.CommandEnvelope) error {
	defer clear(envelope.Parameters)
	bounded, cancel := context.WithDeadline(ctx, envelope.Deadline)
	defer cancel()
	_, err := c.dispatch(bounded, envelope.Method, envelope.Parameters)
	reply, err := adapterevents.NewReply(message.AdapterID, message.ID, struct{}{}, transientSecretError(err))
	if err != nil {
		return err
	}
	return (adapterevents.Client{Bus: c.bus}).Publish(ctx, reply)
}

func (c *Worker) provideTransient(ctx context.Context, params json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var command controllercommands.ProvideTransientSecretCommand
	defer func() { clear(command.Value) }()
	if err := domain.Decode(params, &command); err != nil {
		return domain.ErrInvalidInput
	}
	target, ok := c.controller.(controllercommands.SecretRequests)
	if !ok {
		return secrets.ErrUnavailable
	}
	return target.ProvideTransient(ctx, command.RequestID, command.Value)
}

func transientSecretError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{
		secrets.ErrWriteUncertain, secrets.ErrDeliveryUncertain,
		context.Canceled, context.DeadlineExceeded, domain.ErrControllerClosed,
		secrets.ErrUnavailable, secrets.ErrUnknownRequest, secrets.ErrRequestConflict,
		secrets.ErrRequestClosed, secrets.ErrRequestExpired, domain.ErrInvalidInput,
	} {
		if errors.Is(err, known) {
			return known
		}
	}
	return secrets.ErrDeliveryUncertain
}

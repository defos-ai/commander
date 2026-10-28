package consumers

import (
	"context"
	"errors"
	"fmt"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
)

const logProcessingTimeout = 5 * time.Second

type LogConsumer struct {
	Executions executions.ExecutionStore
}

func (c LogConsumer) Consume(ctx context.Context, message messages.Message) error {
	log, err := controllerevents.DecodeExecutionLog(message)
	if err != nil {
		return InvalidMessage(err)
	}
	ctx, cancel := context.WithTimeout(ctx, logProcessingTimeout)
	defer cancel()
	record, err := c.Executions.Get(ctx, log.AgentID, log.CommandID)
	if errors.Is(err, executions.ErrUnknownExecution) {
		return InvalidMessage(err)
	}
	if err != nil {
		return fmt.Errorf("read logged execution: %w", err)
	}
	if err := record.Validate(); err != nil {
		return fmt.Errorf("invalid logged execution claim: %w", err)
	}
	command := record.Command
	if command.AgentID != log.AgentID || command.ID != log.CommandID || command.Work != log.Work {
		return InvalidMessage(errors.New("execution log does not match its execution claim"))
	}
	var call actions.ActionCall
	if err := domain.Decode(command.Payload, &call); err != nil || call.Work != command.Work {
		return errors.New("invalid claimed action request for execution log")
	}
	if call.ActionName != log.ActionName {
		return InvalidMessage(errors.New("execution log does not match its claimed action"))
	}
	if err := c.Executions.AppendLog(ctx, log); err != nil {
		if errors.Is(err, executions.ErrLogConflict) {
			return InvalidMessage(err)
		}
		return fmt.Errorf("persist execution log: %w", err)
	}
	saved, err := c.Executions.GetLog(ctx, log.ID)
	if err != nil {
		return fmt.Errorf("read saved execution log: %w", err)
	}
	if !saved.Equal(log) {
		return errors.New("saved execution log does not match the accepted observation")
	}
	return nil
}

package fakes

import (
	"bytes"
	"context"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

type CompletionCommands struct {
	commands.Operations
	submitted chan commands.CompleteOperationCommand
	failure   error
}

func NewCompletionCommands(failure error) *CompletionCommands {
	return &CompletionCommands{submitted: make(chan commands.CompleteOperationCommand, 1), failure: failure}
}

func (c *CompletionCommands) Submitted() <-chan commands.CompleteOperationCommand { return c.submitted }

func (c *CompletionCommands) Complete(ctx context.Context, command commands.CompleteOperationCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	command.Output = bytes.Clone(command.Output)
	select {
	case c.submitted <- command:
	case <-ctx.Done():
		return operations.Operation{}, ctx.Err()
	}
	if c.failure != nil {
		return operations.Operation{}, c.failure
	}
	return operations.Operation{ID: command.OperationID, Status: operations.Completed,
		Output: bytes.Clone(command.Output), ReportID: id.ReportID(), CompletedBy: command.CompletedBy}, nil
}

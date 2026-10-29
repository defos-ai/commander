package fakes

import (
	"bytes"
	"context"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

type OperationCompletions struct {
	commands.Operations
	err     error
	command commands.CompleteOperationCommand
	calls   Calls
}

func NewOperationCompletions(err error) *OperationCompletions {
	return &OperationCompletions{err: err}
}

func (s *OperationCompletions) Complete(ctx context.Context, command commands.CompleteOperationCommand) (operations.Operation, error) {
	s.calls.record(ctx)
	s.command = command
	s.command.Output = bytes.Clone(command.Output)
	if s.err != nil {
		return operations.Operation{}, s.err
	}
	return operations.Operation{
		ID: command.OperationID, Status: operations.Completed, ReportID: id.ReportID(),
		Output: bytes.Clone(command.Output), CompletedBy: command.CompletedBy,
	}, nil
}

func (s *OperationCompletions) Snapshot() (commands.CompleteOperationCommand, Calls) {
	command := s.command
	command.Output = bytes.Clone(command.Output)
	return command, s.calls
}

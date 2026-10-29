package fakes

import (
	"bytes"
	"context"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

type CompletionProposals struct {
	commands.Operations
	failure error
	command commands.ProposeCompletionCommand
	calls   Calls
}

func NewCompletionProposals(failure error) *CompletionProposals {
	return &CompletionProposals{failure: failure}
}

func (s *CompletionProposals) ProposeCompletion(ctx context.Context, command commands.ProposeCompletionCommand) (operations.Operation, error) {
	s.calls.record(ctx)
	s.command = command
	s.command.Output = bytes.Clone(command.Output)
	if s.failure != nil {
		return operations.Operation{}, s.failure
	}
	return operations.Operation{
		ID: command.OperationID, Status: operations.Planning,
		CompletionProposal: &operations.CompletionProposal{
			ID: id.New(), Output: bytes.Clone(command.Output), ProposedBy: command.ProposedBy, ProposedAt: time.Now().UTC(),
		},
	}, nil
}

func (s *CompletionProposals) Snapshot() (commands.ProposeCompletionCommand, Calls) {
	command := s.command
	command.Output = bytes.Clone(command.Output)
	return command, s.calls
}

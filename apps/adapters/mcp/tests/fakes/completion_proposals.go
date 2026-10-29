package fakes

import (
	"bytes"
	"context"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

type CompletionProposalCommands struct {
	commands.Operations
	submitted chan commands.ProposeCompletionCommand
}

func NewCompletionProposalCommands() *CompletionProposalCommands {
	return &CompletionProposalCommands{submitted: make(chan commands.ProposeCompletionCommand, 1)}
}

func (c *CompletionProposalCommands) Submitted() <-chan commands.ProposeCompletionCommand {
	return c.submitted
}

func (c *CompletionProposalCommands) ProposeCompletion(ctx context.Context, command commands.ProposeCompletionCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	command.Output = bytes.Clone(command.Output)
	select {
	case c.submitted <- command:
	case <-ctx.Done():
		return operations.Operation{}, ctx.Err()
	}
	return operations.Operation{
		ID: command.OperationID, Status: operations.Planning,
		Mission: missions.Mission{Completion: &missions.CompletionPolicy{DecisionBy: missions.Operator}},
		CompletionProposal: &operations.CompletionProposal{
			ID: id.New(), Output: bytes.Clone(command.Output), ProposedBy: command.ProposedBy, ProposedAt: time.Now().UTC(),
		},
	}, nil
}

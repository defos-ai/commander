package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
)

type SavedCommand struct {
	executions.ExecutionStore
	record executions.Execution
}

func (s SavedCommand) Get(context.Context, string, string) (executions.Execution, error) {
	return s.record, nil
}

func (s SavedCommand) Claim(context.Context, messages.Message) (executions.Execution, bool, error) {
	return s.record, false, nil
}

func NewSavedCommand(record executions.Execution) SavedCommand { return SavedCommand{record: record} }

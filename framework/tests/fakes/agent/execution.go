package fakes

import (
	"context"
	"errors"
	"testing"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/actions"
)

func ExecutionProbe(t *testing.T, observed chan<- action.Execution) action.Executor {
	t.Helper()
	executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
		Action: actions.Action{Name: "inspect-execution"},
		Run: func(ctx context.Context, _ struct{}) (struct{}, error) {
			execution, ok := action.ExecutionFromContext(ctx)
			if !ok {
				return struct{}{}, errors.New("action has no execution identity")
			}
			observed <- execution
			return struct{}{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

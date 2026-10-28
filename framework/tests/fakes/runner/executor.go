package fakes

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/actions"
)

type Executor struct {
	Run     func(context.Context, string, actions.ActionCall) (actions.ActionOutput, error)
	Recover func(context.Context, string, actions.ActionCall) (actions.ActionOutput, error)
}

func (e Executor) RunAction(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
	if e.Run == nil {
		return actions.ActionOutput{}, errors.New("unexpected new dispatch")
	}
	return e.Run(ctx, agent, call)
}
func (e Executor) RecoverAction(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
	if e.Recover == nil {
		return actions.ActionOutput{}, errors.New("unexpected recovery")
	}
	return e.Recover(ctx, agent, call)
}

type Invocation struct {
	Agent string
	Call  actions.ActionCall
	Reply chan actions.ActionOutput
}

func ControlledExecutor() (Executor, <-chan Invocation) {
	calls := make(chan Invocation, 16)
	return Executor{Run: func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		job := Invocation{Agent: agent, Call: call, Reply: make(chan actions.ActionOutput, 1)}
		calls <- job
		select {
		case result := <-job.Reply:
			return result, nil
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
	}}, calls
}

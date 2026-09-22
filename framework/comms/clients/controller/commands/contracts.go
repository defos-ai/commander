// Package commands sends adapter commands to an independently hosted controller over the bus.
// Its contracts are also implemented by the in-process framework services.
package commands

import (
	"context"

	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/operations"
)

type Controller interface {
	DiscoverActions(context.Context, string, *agentmodel.DiscoveryScope) (agentmodel.Agent, error)
	RunAction(context.Context, string, actionmodel.ActionCall) (actionmodel.ActionOutput, error)
	StopAgent(context.Context, string) (agentmodel.Agent, error)
}

type Operations interface {
	Start(context.Context, StartOperationCommand) (operations.Operation, error)
	Resolve(context.Context, ResolveContinuationCommand) (operations.Operation, error)
	Assign(context.Context, AssignTaskCommand) (operations.Task, error)
	Ask(context.Context, AskQuestionCommand) (operations.Question, error)
	Answer(context.Context, AnswerQuestionCommand) (operations.Operation, error)
	Complete(context.Context, CompleteOperationCommand) (operations.Operation, error)
	Fail(context.Context, FailOperationCommand) (operations.Operation, error)
	Cancel(context.Context, CancelOperationCommand) (operations.Operation, error)
}

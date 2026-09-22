package commands

import (
	"context"

	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/operations"
)

var _ Controller = (*Client)(nil)
var _ Operations = (*Client)(nil)

func (c *Client) DiscoverActions(ctx context.Context, agentID string, scope *agentmodel.DiscoveryScope) (agentmodel.Agent, error) {
	var result agentmodel.Agent
	err := c.call(ctx, MethodDiscoverActions, DiscoverActionsCommand{AgentID: agentID, Scope: scope}, &result)
	return result, err
}

func (c *Client) RunAction(ctx context.Context, agentID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	var result actionmodel.ActionOutput
	err := c.call(ctx, MethodRunAction, RunActionCommand{AgentID: agentID, Call: call}, &result)
	return result, err
}

func (c *Client) StopAgent(ctx context.Context, agentID string) (agentmodel.Agent, error) {
	var result agentmodel.Agent
	err := c.call(ctx, MethodStopAgent, StopAgentCommand{AgentID: agentID}, &result)
	return result, err
}

func (c *Client) Start(ctx context.Context, command StartOperationCommand) (operations.Operation, error) {
	var result operations.Operation
	err := c.call(ctx, MethodStartOperation, command, &result)
	return result, err
}

func (c *Client) Assign(ctx context.Context, command AssignTaskCommand) (operations.Task, error) {
	var result operations.Task
	err := c.call(ctx, MethodAssignTask, command, &result)
	return result, err
}

func (c *Client) Ask(ctx context.Context, command AskQuestionCommand) (operations.Question, error) {
	var result operations.Question
	err := c.call(ctx, MethodAskQuestion, command, &result)
	return result, err
}

func (c *Client) Answer(ctx context.Context, command AnswerQuestionCommand) (operations.Operation, error) {
	var result operations.Operation
	err := c.call(ctx, MethodAnswerQuestion, command, &result)
	return result, err
}

func (c *Client) Complete(ctx context.Context, command CompleteOperationCommand) (operations.Operation, error) {
	var result operations.Operation
	err := c.call(ctx, MethodCompleteOperation, command, &result)
	return result, err
}

func (c *Client) Fail(ctx context.Context, command FailOperationCommand) (operations.Operation, error) {
	var result operations.Operation
	err := c.call(ctx, MethodFailOperation, command, &result)
	return result, err
}

func (c *Client) Cancel(ctx context.Context, command CancelOperationCommand) (operations.Operation, error) {
	var result operations.Operation
	err := c.call(ctx, MethodCancelOperation, command, &result)
	return result, err
}

func (c *Client) Resolve(ctx context.Context, command ResolveContinuationCommand) (operations.Operation, error) {
	var result operations.Operation
	err := c.call(ctx, MethodResolveContinuation, command, &result)
	return result, err
}

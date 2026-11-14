package fakes

import (
	"context"
	"encoding/json"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/domain/actions"
)

type ExecutableEnvironment struct {
	environment.Environment
	available bool
}

func (e *ExecutableEnvironment) Install() { e.available = true }

func (e *ExecutableEnvironment) FindExecutable(ctx context.Context, name string, _ *environment.DiscoveryScope) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e.available && name == "lightpanda" {
		return "/fixture/lightpanda", nil
	}
	return "", nil
}

type ExecutableAction struct {
	*action.Base
	executable string
}

func NewExecutableAction(name, executable string) *ExecutableAction {
	return &ExecutableAction{Base: action.NewBase(actions.Action{
		Name: name, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
	}), executable: executable}
}

func (a *ExecutableAction) Discover(ctx context.Context, env environment.Environment, scope *environment.DiscoveryScope) (action.Status, error) {
	if a.executable == "" {
		return a.Base.Discover(ctx, env, scope)
	}
	path, err := env.FindExecutable(ctx, a.executable, scope)
	if err != nil {
		return action.Unknown, err
	}
	if path == "" {
		a.SetUnavailable("Install the browser runtime", actions.Requirement{Kind: actions.ExecutableRequirement, Name: a.executable})
		return action.Unavailable, nil
	}
	a.SetStatus(action.Ready)
	return action.Ready, nil
}

func (*ExecutableAction) Run(context.Context, json.RawMessage) (action.Result, error) {
	return action.JSONResult(struct{}{})
}

type DiscardActionCatalog struct{ actions.ActionStore }

func (DiscardActionCatalog) Upsert(context.Context, actions.Action) error { return nil }

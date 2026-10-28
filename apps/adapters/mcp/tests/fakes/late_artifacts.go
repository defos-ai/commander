package fakes

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
)

type LateArtifactAction struct {
	*action.Base
	started     chan struct{}
	release     chan struct{}
	released    bool
	invocations atomic.Int32
	contents    string
}

func NewLateArtifactAction(contents string) *LateArtifactAction {
	return &LateArtifactAction{Base: action.NewBase(actionmodel.Action{Name: "late-artifact",
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}),
		started: make(chan struct{}, 1), release: make(chan struct{}), contents: contents}
}

func (a *LateArtifactAction) Started() <-chan struct{} { return a.started }

func (a *LateArtifactAction) Invocations() int32 { return a.invocations.Load() }

func (a *LateArtifactAction) Unblock() {
	if !a.released {
		a.released = true
		close(a.release)
	}
}

func (a *LateArtifactAction) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	a.invocations.Add(1)
	select {
	case a.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return action.Result{}, ctx.Err()
	case <-a.release:
	}
	return action.Result{Output: json.RawMessage(`{"built":true}`), Artifacts: []action.Artifact{{Name: "Late source",
		Files: []artifacts.FileSource{{Name: "source.txt", MediaType: "text/plain", Data: []byte(a.contents)}},
	}}}, nil
}

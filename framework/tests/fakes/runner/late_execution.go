package fakes

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

type ObservedOutcome struct {
	Output actions.ActionOutput
	Err    error
}

type LateObservation struct {
	Agent string
	Call  actions.ActionCall
	Reply chan ObservedOutcome
	Done  <-chan struct{}
}

type LateExecution struct {
	dispatches       atomic.Int32
	activeObservers  atomic.Int32
	maximumObservers atomic.Int32
	started          chan Invocation
	observations     chan LateObservation
}

func (e *LateExecution) Executor() Executor {
	return Executor{Run: func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		e.dispatches.Add(1)
		select {
		case e.started <- Invocation{Agent: agent, Call: call}:
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
		<-ctx.Done()
		return actions.ActionOutput{}, ctx.Err()
	}, Recover: func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		active := e.activeObservers.Add(1)
		defer e.activeObservers.Add(-1)
		for maximum := e.maximumObservers.Load(); active > maximum; maximum = e.maximumObservers.Load() {
			if e.maximumObservers.CompareAndSwap(maximum, active) {
				break
			}
		}
		done := make(chan struct{})
		observation := LateObservation{Agent: agent, Call: call, Reply: make(chan ObservedOutcome, 1), Done: done}
		defer close(done)
		select {
		case e.observations <- observation:
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
		select {
		case result := <-observation.Reply:
			return result.Output, result.Err
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
	}}
}

type LateOutcomeCatalog struct {
	runner.AgentCatalog
	unavailable atomic.Bool
}

func (c *LateOutcomeCatalog) Agents(ctx context.Context) ([]agents.Agent, error) {
	if c.unavailable.Load() {
		return nil, errors.New("agent catalog unavailable")
	}
	return c.AgentCatalog.Agents(ctx)
}

func NewLateExecution() *LateExecution {
	return &LateExecution{started: make(chan Invocation, 1), observations: make(chan LateObservation, 16)}
}
func (e *LateExecution) Started() <-chan Invocation           { return e.started }
func (e *LateExecution) Observations() <-chan LateObservation { return e.observations }
func (e *LateExecution) Dispatches() int32                    { return e.dispatches.Load() }
func (e *LateExecution) MaximumObservers() int32              { return e.maximumObservers.Load() }
func (c *LateOutcomeCatalog) SetUnavailable(value bool)       { c.unavailable.Store(value) }

func (e *LateExecution) ActiveObservers() int32 { return e.activeObservers.Load() }

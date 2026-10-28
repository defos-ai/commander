package fakes

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
)

type AgentSourceReadFailure struct {
	agents.AgentSourceStore
	err       error
	reads     atomic.Int32
	remaining atomic.Int64
}

func NewAgentSourceReadFailure(err error) *AgentSourceReadFailure {
	return &AgentSourceReadFailure{err: err}
}

func (store *AgentSourceReadFailure) Reads() (int32, time.Duration) {
	return store.reads.Load(), time.Duration(store.remaining.Load())
}

func (store *AgentSourceReadFailure) List(ctx context.Context) ([]agents.AgentSource, error) {
	store.reads.Add(1)
	deadline, _ := ctx.Deadline()
	store.remaining.Store(int64(time.Until(deadline)))
	return nil, store.err
}

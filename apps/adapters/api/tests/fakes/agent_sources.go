package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/agents"
)

type AgentSourceFailures struct {
	agents.AgentSourceStore
	stage string
	err   error
	calls map[string]Calls
}

func NewAgentSourceFailures(store agents.AgentSourceStore, stage string, err error) *AgentSourceFailures {
	return &AgentSourceFailures{AgentSourceStore: store, stage: stage, err: err, calls: make(map[string]Calls)}
}

func (s *AgentSourceFailures) Create(ctx context.Context, entry agents.AgentSource) error {
	if err := s.record(ctx, "create"); err != nil {
		return err
	}
	return s.AgentSourceStore.Create(ctx, entry)
}

func (s *AgentSourceFailures) Get(ctx context.Context, value string) (agents.AgentSource, error) {
	if err := s.record(ctx, "get"); err != nil {
		return agents.AgentSource{}, err
	}
	return s.AgentSourceStore.Get(ctx, value)
}

func (s *AgentSourceFailures) List(ctx context.Context) ([]agents.AgentSource, error) {
	if err := s.record(ctx, "list"); err != nil {
		return nil, err
	}
	return s.AgentSourceStore.List(ctx)
}

func (s *AgentSourceFailures) Replace(ctx context.Context, entry agents.AgentSource, version uint64) error {
	if err := s.record(ctx, "replace"); err != nil {
		return err
	}
	return s.AgentSourceStore.Replace(ctx, entry, version)
}

func (s *AgentSourceFailures) record(ctx context.Context, stage string) error {
	calls := s.calls[stage]
	calls.record(ctx)
	s.calls[stage] = calls
	if s.stage == stage || s.stage == "*" {
		return s.err
	}
	return nil
}

func (s *AgentSourceFailures) Snapshot(stage string) Calls { return s.calls[stage] }

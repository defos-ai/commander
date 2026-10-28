package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/agents"
)

type CatalogFailures struct {
	agents.CatalogStore
	stage string
	err   error
	calls map[string]Calls
}

func NewCatalogFailures(store agents.CatalogStore, stage string, err error) *CatalogFailures {
	return &CatalogFailures{CatalogStore: store, stage: stage, err: err, calls: make(map[string]Calls)}
}

func (s *CatalogFailures) Create(ctx context.Context, entry agents.CatalogEntry) error {
	if err := s.record(ctx, "create"); err != nil {
		return err
	}
	return s.CatalogStore.Create(ctx, entry)
}

func (s *CatalogFailures) Get(ctx context.Context, value string) (agents.CatalogEntry, error) {
	if err := s.record(ctx, "get"); err != nil {
		return agents.CatalogEntry{}, err
	}
	return s.CatalogStore.Get(ctx, value)
}

func (s *CatalogFailures) List(ctx context.Context) ([]agents.CatalogEntry, error) {
	if err := s.record(ctx, "list"); err != nil {
		return nil, err
	}
	return s.CatalogStore.List(ctx)
}

func (s *CatalogFailures) Replace(ctx context.Context, entry agents.CatalogEntry, version uint64) error {
	if err := s.record(ctx, "replace"); err != nil {
		return err
	}
	return s.CatalogStore.Replace(ctx, entry, version)
}

func (s *CatalogFailures) record(ctx context.Context, stage string) error {
	calls := s.calls[stage]
	calls.record(ctx)
	s.calls[stage] = calls
	if s.stage == stage || s.stage == "*" {
		return s.err
	}
	return nil
}

func (s *CatalogFailures) Snapshot(stage string) Calls { return s.calls[stage] }

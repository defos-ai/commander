package fakes

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
)

type CatalogReadFailure struct {
	agents.CatalogStore
	err       error
	reads     atomic.Int32
	remaining atomic.Int64
}

func NewCatalogReadFailure(err error) *CatalogReadFailure {
	return &CatalogReadFailure{err: err}
}

func (store *CatalogReadFailure) Reads() (int32, time.Duration) {
	return store.reads.Load(), time.Duration(store.remaining.Load())
}

func (store *CatalogReadFailure) List(ctx context.Context) ([]agents.CatalogEntry, error) {
	store.reads.Add(1)
	deadline, _ := ctx.Deadline()
	store.remaining.Store(int64(time.Until(deadline)))
	return nil, store.err
}

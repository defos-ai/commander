package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/sessions"
)

type SessionQueries struct {
	sessions.Store
	queries chan sessions.Query
}

func NewSessionQueries() *SessionQueries {
	return &SessionQueries{queries: make(chan sessions.Query, 1)}
}

func (s *SessionQueries) Queries() <-chan sessions.Query { return s.queries }

func (s *SessionQueries) Find(ctx context.Context, query sessions.Query) ([]sessions.Record, error) {
	select {
	case s.queries <- query:
		return []sessions.Record{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

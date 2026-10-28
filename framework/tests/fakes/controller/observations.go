package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/actions"
)

type ObservationCatalog struct {
	actions.ActionStore
	last    actions.Action
	writes  int
	failure error
}

func (s *ObservationCatalog) Upsert(_ context.Context, entry actions.Action) error {
	if s.failure != nil {
		return s.failure
	}
	s.last, s.writes = entry, s.writes+1
	return nil
}

func (s *ObservationCatalog) Count() int           { return s.writes }
func (s *ObservationCatalog) Last() actions.Action { return s.last.Clone() }
func (s *ObservationCatalog) Fail(err error)       { s.failure = err }

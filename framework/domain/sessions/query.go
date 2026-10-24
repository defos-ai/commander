package sessions

import (
	"context"
	"fmt"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/id"
)

// Query selects retained session records. Nonempty criteria intersect; an empty
// query includes reservations and released identities across every scope.
type Query struct {
	ID           string
	ScopeID      string
	OwnerAgentID string
}

func (q Query) Validate() error {
	if q.ID != "" && !id.Valid(q.ID) {
		return fmt.Errorf("%w: session ID must be a canonical UUID", domain.ErrInvalidInput)
	}
	if q.ScopeID != "" && !id.Valid(q.ScopeID) {
		return fmt.Errorf("%w: scope ID must be a canonical UUID", domain.ErrInvalidInput)
	}
	if q.OwnerAgentID != "" && !id.ValidAgent(q.OwnerAgentID) {
		return fmt.Errorf("%w: owner must identify an exact agent process", domain.ErrInvalidInput)
	}
	return nil
}

func (q Query) Matches(record Record) bool {
	return (q.ID == "" || record.ID == q.ID) &&
		(q.ScopeID == "" || record.ScopeID == q.ScopeID) &&
		(q.OwnerAgentID == "" || record.OwnerAgentID == q.OwnerAgentID)
}

// QueryStore returns independent records ordered by scope ID, then session ID.
// Queries never reserve, release or change saved lifecycle evidence.
type QueryStore interface {
	Find(context.Context, Query) ([]Record, error)
}

package queries

import (
	"context"
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

type Sessions struct {
	Store  sessions.Store
	Agents agents.AgentStore
}

// List projects durable identities even after inventory expires. Reads never
// close, release or reclaim a scope; late observations cannot revive abandonment.
func (q Sessions) List(ctx context.Context, scopeID string) ([]sessions.Session, error) {
	if q.Store == nil {
		return nil, errors.New("session store unavailable")
	}
	scope, err := q.Store.Get(ctx, scopeID)
	if err != nil {
		return nil, err
	}
	records, err := q.Agents.List(ctx)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, r := range records {
		r = r.Snapshot(time.Now().UTC())
		live[r.ID] = !r.Stale && r.Status != agents.AgentStopped
	}
	result := make([]sessions.Session, 0, len(scope.Records))
	for _, r := range scope.Records {
		v := r.View()
		if r.Disposition != sessions.Released && r.OwnerAgentID != "" && !live[r.OwnerAgentID] {
			v.State = "lost"
			v.Reason = "Session owner unavailable; closure unconfirmed"
		}
		result = append(result, v)
	}
	return result, nil
}

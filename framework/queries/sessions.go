package queries

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

type Sessions struct {
	Store  sessions.QueryStore
	Agents agents.AgentStore
}

// List projects durable identities even after inventory expires. Reads never
// close, release or reclaim a scope; late observations cannot revive abandonment.
func (q Sessions) List(ctx context.Context, filter sessions.Query) ([]sessions.Session, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	if q.Store == nil {
		return nil, errors.New("session store unavailable")
	}
	saved, err := q.Store.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(saved) == 0 {
		return []sessions.Session{}, nil
	}
	if q.Agents == nil {
		return nil, errors.New("agent store unavailable")
	}
	records, err := q.Agents.List(ctx)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	now := time.Now().UTC()
	for _, r := range records {
		r = r.Snapshot(now)
		live[r.ID] = !r.Stale && r.Status != agents.AgentStopped
	}
	result := make([]sessions.Session, 0, len(saved))
	for _, r := range saved {
		v := r.View()
		if r.Disposition != sessions.Released && r.OwnerAgentID != "" && !live[r.OwnerAgentID] {
			v.State = "lost"
			v.Reason = "Session owner unavailable; closure unconfirmed"
		}
		result = append(result, v)
	}
	return result, nil
}

func (q Sessions) Get(ctx context.Context, sessionID string) (sessions.Session, error) {
	if !id.Valid(sessionID) {
		return sessions.Session{}, fmt.Errorf("%w: session ID must be a canonical UUID", domain.ErrInvalidInput)
	}
	items, err := q.List(ctx, sessions.Query{ID: sessionID})
	if err != nil {
		return sessions.Session{}, err
	}
	if len(items) == 0 {
		return sessions.Session{}, sessions.ErrUnknownSession
	}
	if len(items) != 1 {
		return sessions.Session{}, sessions.ErrConflict
	}
	return items[0], nil
}

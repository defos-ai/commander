package fixtures

import (
	"time"

	adminsessions "github.com/defos-ai/commander/apps/admin/sessions"
)

const (
	SessionID        = "c4712934-2a88-44e0-8d97-02974966b215"
	SessionScopeID   = "d4712934-2a88-44e0-8d97-02974966b215"
	SessionRequestID = "e4712934-2a88-44e0-8d97-02974966b215"
	SessionOwnerID   = "fixture-agent-00000001"
)

func Session() adminsessions.Session {
	now := time.Now().UTC()
	return adminsessions.Session{ID: SessionID, ScopeID: SessionScopeID, OwnerAgentID: SessionOwnerID, Kind: "fixture", Label: "Test session", State: "connected", Disposition: "active", Revision: 1, CreatedAt: now, UpdatedAt: now}
}

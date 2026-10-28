package sessions

import (
	"time"

	model "github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

type Filter struct {
	ScopeID      string
	OwnerAgentID string
}

func (f Filter) Validate() error {
	return (model.Query{ScopeID: f.ScopeID, OwnerAgentID: f.OwnerAgentID}).Validate()
}

type Session struct {
	ID            string     `json:"id"`
	ScopeID       string     `json:"scope_id"`
	OwnerAgentID  string     `json:"owner_agent_id"`
	Kind          string     `json:"kind"`
	Label         string     `json:"label"`
	State         string     `json:"state"`
	Disposition   string     `json:"disposition"`
	ReleaseReason string     `json:"release_reason,omitempty"`
	Endpoint      string     `json:"endpoint,omitempty"`
	Peer          string     `json:"peer,omitempty"`
	Hostname      string     `json:"hostname,omitempty"`
	User          string     `json:"user,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	Busy          bool       `json:"busy"`
	Revision      uint64     `json:"revision"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ConnectedAt   *time.Time `json:"connected_at,omitempty"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
}

func (s Session) reference() model.Reference {
	return model.Reference{ID: s.ID, ScopeID: s.ScopeID, OwnerAgentID: s.OwnerAgentID}
}

func (s Session) valid() bool {
	if !id.Valid(s.ID) || !id.Valid(s.ScopeID) || s.OwnerAgentID != "" && !id.ValidAgent(s.OwnerAgentID) ||
		s.Kind == "" || s.Label == "" || s.Revision == 0 || s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return false
	}
	switch s.State {
	case "starting", "listening", "verifying", "connected", "unverified", "lost", "closed", "failed":
	default:
		return false
	}
	switch s.Disposition {
	case "reserved", "active", "closing", "released":
	default:
		return false
	}
	switch s.ReleaseReason {
	case "", "closed", "abandoned", "cancelled_before_effect":
	default:
		return false
	}
	return true
}

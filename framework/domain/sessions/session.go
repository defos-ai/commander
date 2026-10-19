// Package sessions describes agent-owned resources that outlive an action task.
package sessions

import (
	"context"
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

type Reference struct {
	ID           string `json:"id"`
	OwnerAgentID string `json:"owner_agent_id"`
	ScopeID      string `json:"scope_id"`
}

type Session struct {
	Reference
	Kind        string     `json:"kind"`
	Label       string     `json:"label"`
	State       string     `json:"state"`
	Endpoint    string     `json:"endpoint,omitempty"`
	Peer        string     `json:"peer,omitempty"`
	Hostname    string     `json:"hostname,omitempty"`
	User        string     `json:"user,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	Busy        bool       `json:"busy"`
	Revision    uint64     `json:"revision"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`
}

func (r Reference) Validate() error {
	if !id.Valid(r.ID) || !id.Valid(r.ScopeID) || !id.ValidAgent(r.OwnerAgentID) {
		return errors.New("session requires canonical session/scope IDs and an owning agent")
	}
	return nil
}

func (s Session) Validate() error {
	if err := s.Reference.Validate(); err != nil {
		return err
	}
	if s.Kind == "" || s.Label == "" || s.Revision == 0 || s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return errors.New("session metadata is incomplete")
	}
	switch s.State {
	case "starting", "listening", "verifying", "connected", "unverified", "lost", "closed", "failed":
		return nil
	default:
		return errors.New("invalid session state")
	}
}

func (s Session) Clone() Session {
	if s.ConnectedAt != nil {
		v := *s.ConnectedAt
		s.ConnectedAt = &v
	}
	if s.VerifiedAt != nil {
		v := *s.VerifiedAt
		s.VerifiedAt = &v
	}
	return s
}

func CloneAll(items []Session) []Session {
	if items == nil {
		return nil
	}
	result := make([]Session, len(items))
	for i := range items {
		result[i] = items[i].Clone()
	}
	return result
}

type Control struct {
	Reference
	Method string `json:"method"`
}

func (r Control) Validate() error {
	if err := r.Reference.Validate(); err != nil {
		return err
	}
	switch r.Method {
	case "inspect", "verify", "close":
		return nil
	default:
		return errors.New("session method must be inspect, verify, or close")
	}
}

// Provider belongs to one process. Snapshots must be quick, concurrency-safe,
// independent copies. Control runs independently of the action command inbox.
// Changes coalesces notifications; every notification publishes a full snapshot.
// An optional Close() error releases transports during owner shutdown, before
// the final inventory announcement. It must retain Lost references; Closed is
// reserved for an explicit close control, which authorizes replacement.
type Provider interface {
	Snapshot() []Session
	Control(context.Context, Control) (Session, error)
	Changes() <-chan struct{}
}

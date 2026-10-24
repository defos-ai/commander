package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

type Mode string

const (
	Create Mode = "create"
	Reuse  Mode = "reuse"
	Use    Mode = "use"
)

// Intent is resolved once from an accepted action contract, never from later inventory.
// Error retains an invalid automatic proposal without rejecting its parent's result.
type Intent struct {
	Mode    Mode   `json:"mode"`
	ScopeID string `json:"scope_id"`
	ID      string `json:"id,omitempty"`
	Error   string `json:"error,omitempty"`
}

func ResolveIntent(mode string, input json.RawMessage) (*Intent, error) {
	if mode == "" {
		return nil, nil
	}
	var target struct {
		ID    string `json:"session_id"`
		Scope string `json:"session_scope"`
	}
	if err := json.Unmarshal(input, &target); err != nil {
		return nil, err
	}
	if !id.Valid(target.Scope) || target.ID != "" && !id.Valid(target.ID) {
		return nil, errors.New("session action requires canonical session_scope and session_id")
	}
	intent := &Intent{Mode: Mode(mode), ScopeID: target.Scope, ID: target.ID}
	if mode == "create" && target.ID != "" {
		intent.Mode = Reuse
	}
	if mode == "use" && target.ID == "" {
		return nil, errors.New("session_id is required")
	}
	if intent.Mode != Create && intent.Mode != Reuse && intent.Mode != Use {
		return nil, errors.New("invalid session mode")
	}
	return intent, nil
}

func (i *Intent) Clone() *Intent {
	if i == nil {
		return nil
	}
	v := *i
	return &v
}
func (i Intent) Validate() error {
	if i.Error != "" {
		return errors.New(i.Error)
	}
	if !id.Valid(i.ScopeID) || !id.Valid(i.ID) {
		return errors.New("session intent requires canonical scope and session IDs")
	}
	if i.Mode != Create && i.Mode != Reuse && i.Mode != Use {
		return errors.New("invalid session intent mode")
	}
	return nil
}

type ExecutionRef struct {
	OperationID string `json:"operation_id"`
	TaskID      string `json:"task_id"`
	CommandID   string `json:"command_id"`
}

func (r ExecutionRef) Validate() error {
	if !id.Valid(r.OperationID) || !id.Valid(r.TaskID) || r.CommandID != r.TaskID {
		return errors.New("session creation requires an operation task and its command")
	}
	return nil
}

type Binding struct {
	Reference
	Mode     Mode         `json:"mode"`
	Creation ExecutionRef `json:"creation"`
}

func (b Binding) Validate() error {
	if err := b.Reference.Validate(); err != nil {
		return err
	}
	if err := b.Creation.Validate(); err != nil {
		return err
	}
	return (Intent{Mode: b.Mode, ScopeID: b.ScopeID, ID: b.ID}).Validate()
}

// CreationClaim lets a provider register an unstarted identity, or tombstone it
// when close wins delivery order. Prepare must never open an external resource.
type CreationClaim struct {
	Reference
	Creation ExecutionRef `json:"creation"`
}

func (c CreationClaim) Validate() error {
	if err := c.Reference.Validate(); err != nil {
		return err
	}
	return c.Creation.Validate()
}
func (b Binding) Claim() CreationClaim {
	return CreationClaim{Reference: b.Reference, Creation: b.Creation}
}

type Disposition string

const (
	Reserved Disposition = "reserved"
	Active   Disposition = "active"
	Closing  Disposition = "closing"
	Released Disposition = "released"
)

type Release struct {
	Reason    string    `json:"reason"`
	RequestID string    `json:"request_id"`
	At        time.Time `json:"at"`
}
type Record struct {
	Reference
	Creation       ExecutionRef `json:"creation"`
	Disposition    Disposition  `json:"disposition"`
	Observation    *Session     `json:"observation,omitempty"`
	PendingControl *Control     `json:"pending_control,omitempty"`
	Release        *Release     `json:"release,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
}

func (r Record) Binding(mode Mode) Binding {
	return Binding{Reference: r.Reference, Mode: mode, Creation: r.Creation}
}
func (r Record) Clone() Record {
	if r.Observation != nil {
		v := r.Observation.Clone()
		r.Observation = &v
	}
	if r.PendingControl != nil {
		v := r.PendingControl.Clone()
		r.PendingControl = &v
	}
	if r.Release != nil {
		v := *r.Release
		r.Release = &v
	}
	return r
}
func (r Record) View() Session {
	v := Session{Reference: r.Reference, Kind: "runtime", Label: "Runtime session", State: "starting", Revision: 1, CreatedAt: r.CreatedAt, UpdatedAt: r.CreatedAt}
	if r.Observation != nil {
		v = r.Observation.Clone()
	}
	v.Disposition = string(r.Disposition)
	if r.Release != nil {
		v.ReleaseReason = r.Release.Reason
		v.State = "closed"
		if r.Release.Reason == "abandoned" {
			v.State = "lost"
			v.Reason = "Session explicitly abandoned; resource closure is unknown"
		}
	}
	return v
}

var (
	ErrUnknownSession   = errors.New("unknown session")
	ErrScopeReserved    = errors.New("scope_reserved: close or abandon the current session before replacing it")
	ErrSessionClosed    = errors.New("session_closed")
	ErrSessionAbandoned = errors.New("session_abandoned")
	ErrSessionClosing   = errors.New("session_closing")
	ErrConflict         = errors.New("session identity or lifecycle conflict")
	ErrWriteUncertain   = errors.New("session write outcome is uncertain")
)

func (r Record) Available() error {
	if r.Disposition == Released {
		if r.Release != nil && r.Release.Reason == "abandoned" {
			return ErrSessionAbandoned
		}
		return ErrSessionClosed
	}
	if r.Disposition == Closing {
		return ErrSessionClosing
	}
	return nil
}

// Scope and its retained identities form one atomic store aggregate. Current may
// change only together with release evidence. Providers own locking/revisions.
type Scope struct {
	ID      string   `json:"id"`
	Current string   `json:"current,omitempty"`
	Records []Record `json:"records"`
}

func (s Scope) Find(sessionID string) (Record, error) {
	for _, r := range s.Records {
		if r.ID == sessionID {
			return r.Clone(), nil
		}
	}
	return Record{}, ErrUnknownSession
}
func (s Scope) Validate() error {
	if !id.Valid(s.ID) {
		return errors.New("invalid session scope")
	}
	seen := map[string]bool{}
	active := ""
	for _, r := range s.Records {
		if !id.Valid(r.ID) || r.ScopeID != s.ID || seen[r.ID] || r.CreatedAt.IsZero() {
			return ErrConflict
		}
		seen[r.ID] = true
		if err := r.Creation.Validate(); err != nil {
			return err
		}
		if r.OwnerAgentID != "" && !id.ValidAgent(r.OwnerAgentID) {
			return ErrConflict
		}
		switch r.Disposition {
		case Reserved, Active, Closing:
			if active != "" || r.Release != nil {
				return ErrConflict
			}
			active = r.ID
		case Released:
			if r.Release == nil || r.Release.At.IsZero() || !id.Valid(r.Release.RequestID) {
				return ErrConflict
			}
			switch r.Release.Reason {
			case "closed":
				if r.Observation == nil || r.Observation.State != "closed" || r.PendingControl == nil || r.PendingControl.RequestID != r.Release.RequestID {
					return ErrConflict
				}
			case "abandoned":
			case "cancelled_before_effect":
				if r.OwnerAgentID != "" {
					return ErrConflict
				}
			default:
				return ErrConflict
			}
		default:
			return ErrConflict
		}
		if r.Disposition == Active && r.Observation == nil || r.Disposition == Closing && r.PendingControl == nil {
			return ErrConflict
		}
		if control := r.PendingControl; control != nil {
			if control.Method != "close" || control.Reference != r.Reference || !id.Valid(control.RequestID) || control.Creation == nil || *control.Creation != r.Creation {
				return ErrConflict
			}
		}
		if r.Observation != nil {
			if err := r.Observation.Validate(); err != nil {
				return err
			}
			if r.Observation.Reference != r.Reference {
				return ErrConflict
			}
		}
	}
	if active != s.Current {
		return ErrConflict
	}
	return nil
}

type ChangeKind string

const (
	Reserve            ChangeKind = "reserve"
	Bind               ChangeKind = "bind"
	Observe            ChangeKind = "observe"
	RequestClose       ChangeKind = "close"
	ConfirmClose       ChangeKind = "closed"
	Abandon            ChangeKind = "abandon"
	CancelBeforeEffect ChangeKind = "cancel_before_effect"
)

type Change struct {
	Kind                      ChangeKind
	ScopeID, ID, OwnerAgentID string
	Creation                  ExecutionRef
	Observation               *Session
	Control                   *Control
	At                        time.Time
}

// Apply is the shared closed set of lifecycle transitions, used under a provider lock.
func (s *Scope) Apply(c Change) error {
	if !id.Valid(c.ScopeID) || !id.Valid(c.ID) || c.At.IsZero() {
		return ErrConflict
	}
	if s.ID == "" {
		s.ID = c.ScopeID
	}
	if s.ID != c.ScopeID {
		return ErrConflict
	}
	index := -1
	for i := range s.Records {
		if s.Records[i].ID == c.ID {
			index = i
			break
		}
	}
	if c.Kind == Reserve {
		if err := c.Creation.Validate(); err != nil {
			return err
		}
		if index >= 0 {
			if s.Records[index].Creation != c.Creation {
				return ErrConflict
			}
			return nil
		}
		if s.Current != "" {
			return ErrScopeReserved
		}
		s.Records = append(s.Records, Record{Reference: Reference{ID: c.ID, ScopeID: c.ScopeID}, Creation: c.Creation, Disposition: Reserved, CreatedAt: c.At})
		s.Current = c.ID
		return s.Validate()
	}
	if index < 0 {
		return ErrUnknownSession
	}
	r := &s.Records[index]
	switch c.Kind {
	case Bind:
		if err := r.Available(); err != nil {
			return err
		}
		if !id.ValidAgent(c.OwnerAgentID) || r.OwnerAgentID != "" && r.OwnerAgentID != c.OwnerAgentID || r.Creation != c.Creation {
			return ErrConflict
		}
		r.OwnerAgentID = c.OwnerAgentID
	case Observe:
		if c.Observation == nil || c.Observation.Reference != r.Reference {
			return ErrConflict
		}
		if err := c.Observation.Validate(); err != nil {
			return err
		}
		if r.Release != nil && r.Release.Reason == "closed" {
			return nil // The persisted close receipt is terminal evidence.
		}
		if r.Observation == nil || c.Observation.Revision > r.Observation.Revision {
			v := c.Observation.Clone()
			v.Disposition = ""
			v.ReleaseReason = ""
			r.Observation = &v
		}
		if r.Disposition == Reserved {
			r.Disposition = Active
		}
	case RequestClose:
		if c.Control == nil || c.Control.Reference != r.Reference || c.Control.Method != "close" || !id.Valid(c.Control.RequestID) {
			return ErrConflict
		}
		if r.Disposition == Released {
			return nil
		}
		if c.Control.Creation == nil || *c.Control.Creation != r.Creation {
			return ErrConflict
		}
		if r.PendingControl == nil {
			v := c.Control.Clone()
			r.PendingControl = &v
		}
		r.Disposition = Closing
	case ConfirmClose:
		if c.Control == nil || r.PendingControl == nil || c.Control.Method != "close" || c.Control.Creation == nil || *c.Control.Creation != r.Creation || c.Control.RequestID != r.PendingControl.RequestID || c.Control.Reference != r.Reference || c.Observation == nil || c.Observation.Reference != r.Reference || c.Observation.State != "closed" {
			return ErrConflict
		}
		if r.Disposition == Released {
			return nil
		}
		if err := c.Observation.Validate(); err != nil {
			return err
		}
		v := c.Observation.Clone()
		r.Observation = &v
		r.Disposition = Released
		r.Release = &Release{Reason: "closed", RequestID: c.Control.RequestID, At: c.At}
		s.Current = ""
	case Abandon, CancelBeforeEffect:
		if r.Disposition == Released {
			return nil
		}
		if c.Control == nil || !id.Valid(c.Control.RequestID) {
			return ErrConflict
		}
		if c.Kind == Abandon && (c.Control.Reference != r.Reference || c.Control.Method != "abandon") {
			return ErrConflict
		}
		reason := "abandoned"
		if c.Kind == CancelBeforeEffect {
			if r.OwnerAgentID != "" {
				return ErrConflict
			}
			reason = "cancelled_before_effect"
		}
		r.Disposition = Released
		r.Release = &Release{Reason: reason, RequestID: c.Control.RequestID, At: c.At}
		s.Current = ""
	default:
		return fmt.Errorf("unknown session change %q", c.Kind)
	}
	return s.Validate()
}

type Store interface {
	QueryStore
	Get(context.Context, string) (Scope, error)
	List(context.Context) ([]Scope, error)
	Apply(context.Context, Change) (Record, error)
}

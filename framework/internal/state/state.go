// Package state holds the controller state shared by commands and consumers.
package state

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"commander/framework/artifact"
	"commander/framework/comms/messages"
	"commander/framework/domain"
	agentmodel "commander/framework/domain/agents"
)

var (
	ErrControllerClosed = domain.ErrControllerClosed
	ErrAgentNotRunning  = agentmodel.ErrAgentNotRunning
)

type CommandReply struct {
	Message messages.Message
	Record  agentmodel.Agent
	Err     error
}

type PendingCommand struct {
	AgentID  string
	Work     messages.WorkContext
	Expected messages.MessageType
	Reply    chan CommandReply
}

// State is local to one controller. Mu protects its mutable fields. Consumers
// and command callers share it so completion cannot race caller registration.
type State struct {
	ObservationMu    sync.Mutex // serializes inventory observations with lease expiry
	Mu               sync.RWMutex
	Context          context.Context
	HeartbeatTimeout time.Duration
	Closed           bool
	Revision         uint64
	Changed          chan struct{}
	Pending          map[string]*PendingCommand
	Artifacts        map[string]artifact.Ref
}

func New(ctx context.Context, heartbeatTimeout time.Duration) *State {
	return &State{
		Context: ctx, HeartbeatTimeout: heartbeatTimeout,
		Changed: make(chan struct{}), Pending: make(map[string]*PendingCommand),
		Artifacts: make(map[string]artifact.Ref),
	}
}

func (s *State) CheckOpen() error {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	if s.Closed || s.Context.Err() != nil {
		return s.ClosedError()
	}
	return nil
}

func (s *State) ClosedError() error {
	cause := context.Cause(s.Context)
	if cause == nil {
		return ErrControllerClosed
	}
	if errors.Is(cause, ErrControllerClosed) {
		return cause
	}
	return errors.Join(ErrControllerClosed, cause)
}

// NotifyLocked wakes inventory waiters. The caller must hold Mu for writing.
func (s *State) NotifyLocked() {
	s.Revision++
	close(s.Changed)
	s.Changed = make(chan struct{})
}

// FailAgentLocked completes outstanding calls for an unavailable agent. The
// caller must hold Mu for writing.
func (s *State) FailAgentLocked(agentID string, err error) {
	for commandID, pending := range s.Pending {
		if pending.AgentID == agentID {
			delete(s.Pending, commandID)
			pending.Reply <- CommandReply{Err: err}
		}
	}
}

func (s *State) Snapshot(record agentmodel.Agent, now time.Time) agentmodel.Agent {
	if record.LeaseExpiresAt.IsZero() {
		record.LeaseExpiresAt = record.LastHeartbeat.Add(s.HeartbeatTimeout)
	}
	return record.Snapshot(now)
}

// RestoreArtifacts validates the whole batch before making any references
// available. Storage remains responsible for checking content when read.
func (s *State) RestoreArtifacts(refs []artifact.Ref) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.Closed || s.Context.Err() != nil {
		return s.ClosedError()
	}
	validated := make(map[string]artifact.Ref, len(refs))
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("restore artifact: %w", err)
		}
		if previous, exists := s.Artifacts[ref.ID]; exists && previous != ref {
			return fmt.Errorf("artifact %s has conflicting metadata", ref.ID)
		}
		if previous, exists := validated[ref.ID]; exists && previous != ref {
			return fmt.Errorf("artifact %s has conflicting metadata", ref.ID)
		}
		validated[ref.ID] = ref
	}
	for id, ref := range validated {
		s.Artifacts[id] = ref
	}
	return nil
}

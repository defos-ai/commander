package state

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
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
	Context  context.Context
	AgentID  string
	Work     messages.WorkContext
	Expected messages.MessageType
	Reply    chan CommandReply
}

// The controller loop owns Pending and all mutations. Readers only access the
// immutable published view or the controller context.
type State struct {
	Context          context.Context
	HeartbeatTimeout time.Duration
	Pending          map[string]*PendingCommand
	view             atomic.Pointer[view]
}

type view struct {
	changed chan struct{}
}

func New(ctx context.Context, heartbeatTimeout time.Duration) *State {
	s := &State{Context: ctx, HeartbeatTimeout: heartbeatTimeout, Pending: make(map[string]*PendingCommand)}
	s.view.Store(&view{changed: make(chan struct{})})
	return s
}

func (s *State) CheckOpen() error {
	if s.Context.Err() != nil {
		return s.ClosedError()
	}
	return nil
}

func (s *State) Changed() <-chan struct{} { return s.view.Load().changed }

func (s *State) Close() {
	for id, pending := range s.Pending {
		delete(s.Pending, id)
		pending.Reply <- CommandReply{Err: s.ClosedError()}
	}
	close(s.view.Load().changed)
}

// Callers cancel their wait independently of the loop. Reap them before the next
// request or lease sweep, so cancellation never waits for a slow store or broker.
func (s *State) PrunePending() {
	for id, pending := range s.Pending {
		if pending.Context.Err() != nil {
			delete(s.Pending, id)
		}
	}
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

func (s *State) Notify() {
	previous := s.view.Load()
	s.view.Store(&view{changed: make(chan struct{})})
	close(previous.changed)
}

func (s *State) FailAgent(agentID string, err error) {
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

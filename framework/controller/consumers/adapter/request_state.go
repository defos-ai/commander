package adapter

import (
	"bytes"
	"context"
	"errors"
	"time"

	"commander/framework/comms"
	"commander/framework/comms/messages"
)

type requestState struct {
	message messages.Message
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	reply   messages.Message // immutable after done closes
	err     error
}

type requestLedger struct {
	active    map[string]*requestState
	canceled  map[string]string
	completed []string
}

type requestCoordinator struct {
	claims        chan requestClaim
	completions   chan requestCompletion
	cancellations chan requestCancellation
}

type requestClaim struct {
	ctx      context.Context
	message  messages.Message
	deadline time.Time
	reply    chan claimedRequest
}

type claimedRequest struct {
	state   *requestState
	execute bool
	err     error
}

type requestCompletion struct {
	state *requestState
	reply messages.Message
	err   error
}

type requestCancellation struct {
	message messages.Message
	done    chan struct{}
}

func (l *requestLedger) claim(claim requestClaim) {
	m := claim.message
	if previous := l.active[m.ID]; previous != nil {
		if previous.message.AdapterID != m.AdapterID || !bytes.Equal(previous.message.Payload, m.Payload) {
			claim.reply <- claimedRequest{err: comms.InvalidMessage(errors.New("request ID reused with different parameters"))}
			return
		}
		claim.reply <- claimedRequest{state: previous}
		return
	}
	ctx, cancel := context.WithDeadline(claim.ctx, claim.deadline)
	m.Payload = bytes.Clone(m.Payload)
	state := &requestState{message: m, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	l.active[m.ID] = state
	if l.canceled[m.ID] == m.AdapterID {
		cancel()
	}
	delete(l.canceled, m.ID)
	claim.reply <- claimedRequest{state: state, execute: true}
}

func (l *requestLedger) complete(completion requestCompletion) {
	state := completion.state
	state.cancel()
	state.cancel = nil
	state.reply, state.err = completion.reply, completion.err
	close(state.done)
	l.completed = append(l.completed, state.message.ID)
	if len(l.completed) > 4096 {
		delete(l.active, l.completed[0])
		l.completed = l.completed[1:]
	}
}

func (l *requestLedger) cancel(cancellation requestCancellation) {
	defer close(cancellation.done)
	m := cancellation.message
	if state := l.active[m.CorrelationID]; state != nil {
		if state.message.AdapterID == m.AdapterID && state.cancel != nil {
			state.cancel()
		}
		return
	}
	// A cancellation can overtake a queued request. Its deadline remains the
	// fallback if this bounded collection has already evicted the notice.
	if len(l.canceled) >= 4096 {
		for requestID := range l.canceled {
			delete(l.canceled, requestID)
			break
		}
	}
	l.canceled[m.CorrelationID] = m.AdapterID
}

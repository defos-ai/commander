package adapter

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

const requestLedgerCapacity = 4096

type requestState struct {
	message  messages.Message
	deadline time.Time
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	reply    messages.Message // immutable after done closes
	err      error
}

type requestLedger struct {
	active   map[string]*requestState
	canceled map[string]string
	changed  chan struct{}
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
	state     *requestState
	execute   bool
	err       error
	available <-chan struct{}
	retryAt   time.Time
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

func (c requestCoordinator) claim(ctx context.Context, message messages.Message, deadline time.Time) claimedRequest {
	for {
		claim := requestClaim{ctx: ctx, message: message, deadline: deadline, reply: make(chan claimedRequest, 1)}
		select {
		case c.claims <- claim:
		case <-ctx.Done():
			return claimedRequest{err: ctx.Err()}
		}
		// An accepted claim must receive completion, even if cancellation raced
		// with registration. Admission waiters have not reserved execution yet.
		result := <-claim.reply
		if result.available == nil {
			return result
		}
		wakeAt := deadline
		if !result.retryAt.IsZero() && result.retryAt.Before(wakeAt) {
			wakeAt = result.retryAt
		}
		timer := time.NewTimer(time.Until(wakeAt))
		select {
		case <-result.available:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return claimedRequest{err: ctx.Err()}
		}
		timer.Stop()
	}
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
	now := time.Now()
	retain := claim.deadline.After(now)
	if retain && len(l.active) >= requestLedgerCapacity {
		retryAt := l.prune(now)
		if len(l.active) >= requestLedgerCapacity {
			if l.changed == nil {
				l.changed = make(chan struct{})
			}
			claim.reply <- claimedRequest{available: l.changed, retryAt: retryAt}
			return
		}
	}
	ctx, cancel := context.WithDeadline(claim.ctx, claim.deadline)
	m.Payload = bytes.Clone(m.Payload)
	state := &requestState{message: m, deadline: claim.deadline, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	// Expired unseen requests only produce a timeout reply; dispatch is skipped
	// by their expired context, so they do not need a retained ledger entry.
	if retain {
		l.active[m.ID] = state
	}
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
	if l.active[state.message.ID] == state {
		l.wakeAdmission()
	}
}

func (l *requestLedger) prune(now time.Time) time.Time {
	var nextExpiry time.Time
	for requestID, state := range l.active {
		select {
		case <-state.done:
		default:
			continue // An expired context does not prove that execution has stopped.
		}
		if !state.deadline.After(now) {
			delete(l.active, requestID)
			l.wakeAdmission()
		} else if nextExpiry.IsZero() || state.deadline.Before(nextExpiry) {
			nextExpiry = state.deadline
		}
	}
	return nextExpiry
}

func (l *requestLedger) wakeAdmission() {
	if l.changed != nil {
		close(l.changed)
		l.changed = nil
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
	if len(l.canceled) >= requestLedgerCapacity {
		for requestID := range l.canceled {
			delete(l.canceled, requestID)
			break
		}
	}
	l.canceled[m.CorrelationID] = m.AdapterID
}

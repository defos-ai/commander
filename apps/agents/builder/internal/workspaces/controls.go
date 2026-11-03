package workspaces

import (
	"context"
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/domain/sessions"
)

func (p *Provider) Control(ctx context.Context, request sessions.Control) (sessions.Session, error) {
	if err := request.Validate(); err != nil {
		return sessions.Session{}, err
	}
	if request.Method == "abandon" {
		return sessions.Session{}, errors.New("abandonment is controller-owned")
	}
	if request.Creation != nil {
		if err := (sessions.CreationClaim{Reference: request.Reference, Creation: *request.Creation}).Validate(); err != nil {
			return sessions.Session{}, err
		}
	}
	p.mu.Lock()
	item := p.records[request.ID]
	if item == nil && request.Method == "close" && request.Creation != nil {
		item = newRecord(sessions.CreationClaim{Reference: request.Reference, Creation: *request.Creation})
		p.records[request.ID] = item
	}
	if item == nil {
		p.mu.Unlock()
		return sessions.Session{}, sessions.ErrUnknownSession
	}
	if item.claim.Reference != request.Reference || request.Creation != nil && item.claim.Creation != *request.Creation {
		p.mu.Unlock()
		return sessions.Session{}, sessions.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return sessions.Session{}, err
	}
	if request.Method != "close" {
		if request.Method == "verify" && !item.closing && item.session.State == "connected" {
			if _, err := item.workspace.Source(); err != nil {
				p.mu.Unlock()
				return sessions.Session{}, err
			}
			now := time.Now().UTC()
			item.session.VerifiedAt = &now
			p.changed(item)
		}
		observed := item.session.Clone()
		p.mu.Unlock()
		return observed, nil
	}
	// Fence starts before waiting: an expired close must not reopen this claim.
	item.closing = true
	if item.cancel != nil {
		item.cancel()
	}
	done := item.done
	p.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return sessions.Session{}, ctx.Err()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if item.session.State != "closed" {
		item.session.State, item.session.Busy = "closed", false
		p.changed(item)
	}
	return item.session.Clone(), nil
}

func (p *Provider) Close() error {
	p.mu.Lock()
	p.stopped = true
	var pending []<-chan struct{}
	for _, item := range p.records {
		if item.cancel != nil {
			item.cancel()
			pending = append(pending, item.done)
		}
	}
	p.mu.Unlock()
	for _, done := range pending {
		<-done
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, item := range p.records {
		if item.session.State != "closed" {
			item.session.State, item.session.Busy = "lost", false
			p.changed(item)
		}
	}
	return nil
}

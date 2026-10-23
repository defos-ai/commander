package sessions

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	model "github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func (c *Coordinator) Control(ctx context.Context, request model.Control) (model.Session, error) {
	if err := request.Validate(); err != nil {
		return model.Session{}, err
	}
	if request.RequestID == "" {
		request.RequestID = id.New()
	}
	if err := c.lock(ctx); err != nil {
		return model.Session{}, err
	}
	record, err := c.controlIntent(ctx, request)
	c.unlock()
	if err != nil {
		return model.Session{}, err
	}
	if record.Disposition == model.Released {
		return record.View(), nil
	}
	if request.Method == "abandon" {
		return record.View(), nil
	}
	if request.Method == "inspect" {
		return c.view(ctx, record)
	}
	if record.PendingControl != nil && request.Method == "close" {
		request = *record.PendingControl
	}
	creation := record.Creation
	request.Creation = &creation
	observation, err := c.remote(ctx, request)
	if err != nil {
		return model.Session{}, err
	}
	if err := c.lock(ctx); err != nil {
		return model.Session{}, err
	}
	defer c.unlock()
	kind := model.Observe
	if request.Method == "close" {
		kind = model.ConfirmClose
	}
	record, err = c.apply(ctx, model.Change{Kind: kind, ScopeID: request.ScopeID, ID: request.ID, Control: &request, Observation: &observation})
	if err != nil {
		return model.Session{}, err
	}
	return record.View(), nil
}
func (c *Coordinator) controlIntent(ctx context.Context, request model.Control) (model.Record, error) {
	scope, err := c.store.Get(ctx, request.ScopeID)
	if err != nil {
		return model.Record{}, err
	}
	record, err := scope.Find(request.ID)
	if err != nil {
		return record, err
	}
	if record.Reference != request.Reference {
		return record, model.ErrConflict
	}
	if record.Disposition == model.Released {
		switch request.Method {
		case "close":
			return c.apply(ctx, model.Change{Kind: model.RequestClose, ScopeID: request.ScopeID, ID: request.ID, Control: &request})
		case "abandon":
			return c.apply(ctx, model.Change{Kind: model.Abandon, ScopeID: request.ScopeID, ID: request.ID, Control: &request})
		}
		return record, nil
	}
	switch request.Method {
	case "close":
		creation := record.Creation
		request.Creation = &creation
		return c.apply(ctx, model.Change{Kind: model.RequestClose, ScopeID: request.ScopeID, ID: request.ID, Control: &request})
	case "inspect":
		return record, nil
	case "abandon":
		view, err := c.view(ctx, record)
		if err != nil {
			return record, err
		}
		if view.State != "lost" && view.State != "failed" {
			return record, errors.New("only a lost session can be abandoned")
		}
		return c.apply(ctx, model.Change{Kind: model.Abandon, ScopeID: request.ScopeID, ID: request.ID, Control: &request})
	default:
		return record, record.Available()
	}
}
func (c *Coordinator) view(ctx context.Context, record model.Record) (model.Session, error) {
	view := record.View()
	if record.Disposition == model.Released {
		return view, nil
	}
	if record.OwnerAgentID == "" {
		return view, nil
	}
	agent, err := c.agents.Get(ctx, record.OwnerAgentID)
	if err != nil && !errors.Is(err, agents.ErrUnknownAgent) {
		return view, err
	}
	agent = agent.Snapshot(time.Now().UTC())
	if err != nil || agent.Stale || agent.Status == agents.AgentStopped {
		view.State = "lost"
		view.Reason = "Session owner is unavailable; closure is unconfirmed"
	}
	return view, nil
}

// ResumeControls retries saved close identities and best-effort revocation after
// abandonment. An unaware unreachable owner may still run previously delivered work.
func (c *Coordinator) ResumeControls(ctx context.Context) error {
	scopes, err := c.store.List(ctx)
	if err != nil {
		return err
	}

	var requests []model.Record
	for _, scope := range scopes {
		for _, record := range scope.Records {
			if record.Disposition == model.Closing && record.PendingControl != nil || record.Release != nil && record.Release.Reason == "abandoned" && (record.Observation == nil || record.Observation.State != "closed") {
				requests = append(requests, record)
			}
		}
	}
	jobs := make(chan model.Record)
	var workers sync.WaitGroup
	for range min(4, len(requests)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for record := range jobs {
				bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
				if record.Disposition == model.Closing {
					_, _ = c.Control(bounded, *record.PendingControl)
				} else {
					request := model.Control{Reference: record.Reference, Method: "close", RequestID: record.Release.RequestID, Creation: &record.Creation}
					observation, err := c.remote(bounded, request)
					if err == nil {
						if err = c.lock(bounded); err == nil {
							_ = c.observeResource(bounded, observation)
							c.unlock()
						}
					}
				}
				cancel()
			}
		}()
	}
	for _, record := range requests {
		select {
		case jobs <- record:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()

	return ctx.Err()
}

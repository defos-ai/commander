package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	model "github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func (w *Worker) Control(ctx context.Context, request model.Control) (model.Session, error) {
	if err := request.Validate(); err != nil {
		return model.Session{}, err
	}
	if request.RequestID == "" {
		request.RequestID = id.New()
	}
	if err := w.lock(ctx); err != nil {
		return model.Session{}, err
	}
	record, err := w.controlIntent(ctx, request)
	w.unlock()
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
		return w.view(ctx, record)
	}
	if record.PendingControl != nil && request.Method == "close" {
		request = *record.PendingControl
	}
	request.CommandID = record.CommandID
	request.OperationID = record.OperationID
	request.TaskID = record.TaskID
	observation, err := w.remote.ControlSessionRemote(ctx, request)
	if err != nil {
		return model.Session{}, err
	}
	if err := w.lock(ctx); err != nil {
		return model.Session{}, err
	}
	defer w.unlock()
	kind := model.Observe
	if request.Method == "close" {
		kind = model.ConfirmClose
	}
	record, err = w.apply(ctx, model.Change{Kind: kind, ScopeID: request.ScopeID, ID: request.ID, Control: &request, Observation: &observation})
	if err != nil {
		return model.Session{}, err
	}
	return record.View(), nil
}
func (w *Worker) controlIntent(ctx context.Context, request model.Control) (model.Record, error) {
	scope, err := w.store.Get(ctx, request.ScopeID)
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
			return w.apply(ctx, model.Change{Kind: model.RequestClose, ScopeID: request.ScopeID, ID: request.ID, Control: &request})
		case "abandon":
			return w.apply(ctx, model.Change{Kind: model.Abandon, ScopeID: request.ScopeID, ID: request.ID, Control: &request})
		}
		return record, nil
	}
	switch request.Method {
	case "close":
		request.CommandID = record.CommandID
		request.OperationID = record.OperationID
		request.TaskID = record.TaskID
		return w.apply(ctx, model.Change{Kind: model.RequestClose, ScopeID: request.ScopeID, ID: request.ID, Control: &request})
	case "inspect":
		return record, nil
	case "abandon":
		view, err := w.view(ctx, record)
		if err != nil {
			return record, err
		}
		if view.State != "lost" && view.State != "failed" {
			return record, errors.New("only a lost session can be abandoned")
		}
		return w.apply(ctx, model.Change{Kind: model.Abandon, ScopeID: request.ScopeID, ID: request.ID, Control: &request})
	default:
		return record, record.Available()
	}
}
func (w *Worker) view(ctx context.Context, record model.Record) (model.Session, error) {
	view := record.View()
	if record.Disposition == model.Released {
		return view, nil
	}
	if record.OwnerAgentID == "" {
		return view, nil
	}
	agent, err := w.agents.Get(ctx, record.OwnerAgentID)
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
func (w *Worker) ResumeControls(ctx context.Context) error {
	scopes, err := w.store.List(ctx)
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
					_, _ = w.Control(bounded, *record.PendingControl)
				} else {
					request := model.Control{Reference: record.Reference, Method: "close", RequestID: record.Release.RequestID,
						CommandID: record.CommandID, OperationID: record.OperationID, TaskID: record.TaskID}
					observation, err := w.remote.ControlSessionRemote(bounded, request)
					if err == nil {
						if err = w.lock(bounded); err == nil {
							_ = w.observeResource(bounded, observation)
							w.unlock()
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

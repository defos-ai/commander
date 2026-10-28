package sessionworker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

type Resources struct {
	ctx     context.Context
	peer    string
	state   string
	mu      sync.Mutex
	items   []sessions.Session
	claims  map[string]sessions.CreationClaim
	closed  map[string]bool
	changes chan struct{}
}

func (r *Resources) Snapshot() []sessions.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sessions.CloneAll(r.items)
}
func (r *Resources) Changes() <-chan struct{} { return r.changes }
func (r *Resources) Prepare(_ context.Context, claim sessions.CreationClaim) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := claim.Validate(); err != nil {
		return err
	}
	if r.closed[claim.ID] {
		return errors.New("session closed")
	}
	if previous, ok := r.claims[claim.ID]; ok && previous != claim {
		return errors.New("claim conflict")
	}
	r.claims[claim.ID] = claim
	return nil
}
func (r *Resources) Control(ctx context.Context, request sessions.Control) (sessions.Session, error) {
	if request.Method == "close" {
		probe, err := http.NewRequestWithContext(ctx, http.MethodPost, r.peer+"/close/"+request.ID, nil)
		if err != nil {
			return sessions.Session{}, err
		}
		response, err := http.DefaultClient.Do(probe)
		if err != nil {
			return sessions.Session{}, err
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			return sessions.Session{}, errors.New("fixture closure unconfirmed")
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if request.Method == "close" {
		if request.Creation == nil {
			return sessions.Session{}, errors.New("creation claim required")
		}
		claim := sessions.CreationClaim{Reference: request.Reference, Creation: *request.Creation}
		if old, ok := r.claims[claim.ID]; ok && old != claim {
			return sessions.Session{}, errors.New("claim conflict")
		}
		r.claims[claim.ID] = claim
		r.closed[claim.ID] = true
	}
	for i := range r.items {
		if r.items[i].Reference == request.Reference {
			if request.Method == "close" {
				r.items[i].State = "closed"
				r.items[i].Revision++
				r.items[i].UpdatedAt = time.Now().UTC()
			}
			if request.Method == "verify" {
				r.items[i].State = "connected"
				r.items[i].Revision++
				r.items[i].UpdatedAt = time.Now().UTC()
			}
			return r.items[i].Clone(), nil
		}
	}
	if request.Method == "close" {
		now := time.Now().UTC()
		item := sessions.Session{Reference: request.Reference, Kind: "fixture", Label: "Owned E2E resource", State: "closed", Revision: 2, CreatedAt: now, UpdatedAt: now}
		r.items = append(r.items, item)
		return item, nil
	}
	return sessions.Session{}, errors.New("unknown resource")
}

type Action struct {
	*action.Base
	owner, peer string
	resources   *Resources
}

func (a *Action) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
	if a.Action().Name == "propose-work" {
		var input struct {
			ActionName string `json:"action_name"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return action.Result{}, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.peer+"/propose", nil)
		if err != nil {
			return action.Result{}, err
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return action.Result{}, err
		}
		response.Body.Close()
		return action.Result{Output: json.RawMessage(`{}`), NextActions: []action.NextAction{{ActionName: input.ActionName, Input: json.RawMessage(`{}`)}}}, nil
	}
	if a.Action().Name == "propose-connections" {
		return action.Result{Output: json.RawMessage(`{}`), NextActions: []action.NextAction{
			{ActionName: "connect-resource", Input: raw},
			{ActionName: "connect-resource", Input: raw},
		}}, nil
	}
	binding, ok := action.SessionFromContext(ctx)
	if !ok {
		return action.Result{}, errors.New("missing framework binding")
	}
	if binding.Mode == sessions.Reuse {
		for _, item := range a.resources.Snapshot() {
			if item.Reference == binding.Reference {
				return action.Result{Output: json.RawMessage(`{"reused":true}`), Session: &item}, nil
			}
		}
		return action.Result{}, errors.New("missing reuse resource")
	}
	intent, _ := json.Marshal(map[string]string{"session_id": binding.ID, "owner": binding.OwnerAgentID})
	if err := action.SaveCheckpoint(ctx, "session_setup_intent", intent); err != nil {
		return action.Result{}, err
	}
	if err := action.SaveCheckpoint(ctx, "session_setup_intent", intent); err != nil {
		return action.Result{}, err
	}
	if err := action.SaveCheckpoint(ctx, "session_setup_intent", json.RawMessage(`{"conflict":true}`)); !errors.Is(err, executions.ErrCheckpointConflict) {
		return action.Result{}, errors.New("checkpoint mutation was not rejected")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.peer+"/create/"+a.owner+"/"+binding.ID, nil)
	if err != nil {
		return action.Result{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return action.Result{}, err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return action.Result{}, errors.New("fixture rejected creation")
	}

	a.resources.mu.Lock()
	if a.resources.closed[binding.ID] {
		a.resources.mu.Unlock()
		return action.Result{}, errors.New("resource closed while starting")
	}
	now := time.Now().UTC()
	resource := sessions.Session{Reference: binding.Reference, Kind: "fixture", Label: "Owned E2E resource", State: "connected", Revision: 1, CreatedAt: now, UpdatedAt: now, VerifiedAt: &now}
	if a.resources.state == "listening" {
		resource.State, resource.VerifiedAt = "listening", nil
	}
	a.resources.mu.Unlock()
	// The accepted task result can precede the first inventory announcement.
	go func() {
		request, err := http.NewRequestWithContext(a.resources.ctx, http.MethodGet, a.peer+"/inventory", nil)
		if err != nil {
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return
		}
		response.Body.Close()
		a.resources.mu.Lock()
		if !a.resources.closed[resource.ID] {
			a.resources.items = append(a.resources.items, resource)
		}
		a.resources.mu.Unlock()
		select {
		case a.resources.changes <- struct{}{}:
		case <-a.resources.ctx.Done():
		}
	}()
	if err := action.SaveCheckpoint(ctx, "session_setup_observed", json.RawMessage(`{"connected":true}`)); err != nil {
		return action.Result{}, err
	}
	saved, found, err := action.LoadCheckpoint(ctx, "session_setup_intent")
	if err != nil || !found || string(saved) != string(intent) {
		return action.Result{}, errors.New("checkpoint did not survive setup")
	}
	output, _ := json.Marshal(map[string]any{"checkpoint_immutable": true, "session_id": resource.ID, "owner": a.owner})
	return action.Result{Output: output, Session: &resource}, nil
}

func NewResources(ctx context.Context, peer, state string) *Resources {
	return &Resources{ctx: ctx, peer: peer, state: state, claims: map[string]sessions.CreationClaim{}, closed: map[string]bool{}, changes: make(chan struct{}, 8)}
}

func NewAction(definition actions.Action, owner, peer string, resources *Resources) *Action {
	return &Action{Base: action.NewBase(definition), owner: owner, peer: peer, resources: resources}
}

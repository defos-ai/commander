// sessionworker is an executable E2E extension. Its HTTP peer controls when
// listener creation returns and when that resource enters public inventory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	frameworkcomms "github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	rabbitcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
)

type resources struct {
	ctx     context.Context
	peer    string
	mu      sync.Mutex
	items   []sessions.Session
	claims  map[string]sessions.CreationClaim
	closed  map[string]bool
	changes chan struct{}
}

func (r *resources) Snapshot() []sessions.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sessions.CloneAll(r.items)
}
func (r *resources) Changes() <-chan struct{} { return r.changes }
func (r *resources) Prepare(_ context.Context, claim sessions.CreationClaim) error {
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
func (r *resources) Control(ctx context.Context, request sessions.Control) (sessions.Session, error) {
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

type fixtureAction struct {
	*action.Base
	owner, peer string
	resources   *resources
}

func (a *fixtureAction) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
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
	if os.Getenv("FIXTURE_SESSION_STATE") == "listening" {
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

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var bus frameworkcomms.Bus
	var err error
	busURL := os.Getenv("BUS_URL")
	if strings.HasPrefix(busURL, "amqp") {
		bus, err = rabbitcomms.NewRabbitMQBus(ctx, busURL, rabbitcomms.Options{})
	} else {
		bus, err = fscomms.NewFsBus(busURL)
	}
	if err != nil {
		return err
	}
	defer bus.Close()
	files, err := artifacts.NewArtifactFileFsStore(os.Getenv("STORAGE_URL"))
	if err != nil {
		return err
	}
	owner := os.Getenv("FIXTURE_AGENT_ID")
	resources := &resources{ctx: ctx, peer: os.Getenv("FIXTURE_PEER"), claims: map[string]sessions.CreationClaim{}, closed: map[string]bool{}, changes: make(chan struct{}, 8)}
	var executors []action.Executor
	for _, name := range []string{"propose-connections", "connect-resource", "propose-work"} {
		mode := ""
		if name == "connect-resource" {
			mode = "create"
		}
		inputSchema := json.RawMessage(`{"type":"object","required":["session_scope"],"properties":{"session_scope":{"type":"string"}}}`)
		if name == "propose-work" {
			inputSchema = json.RawMessage(`{"type":"object","required":["action_name"],"properties":{"action_name":{"type":"string"}}}`)
		}
		executors = append(executors, &fixtureAction{Base: action.NewBase(actions.Action{
			Name: name, Description: "E2E connection fixture", SessionMode: mode,
			InputSchema:  inputSchema,
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		}), owner: owner, peer: os.Getenv("FIXTURE_PEER"), resources: resources})
	}
	type useInput struct {
		Scope string `json:"session_scope"`
		ID    string `json:"session_id"`
	}
	type useOutput struct {
		Owner        string `json:"owner"`
		ID           string `json:"session_id"`
		CreationTask string `json:"creation_task"`
		Isolated     bool   `json:"checkpoint_isolated"`
	}
	secrets := []string(nil)
	if os.Getenv("FIXTURE_USE_REQUIRES_SECRET") == "true" {
		secrets = []string{"SESSION_ACCESS"}
	}
	use, err := action.NewFunc(action.FuncConfig[useInput, useOutput]{Action: actions.Action{Name: "use-resource", SessionMode: "use", RequiredSecrets: secrets}, Run: func(ctx context.Context, input useInput) (useOutput, error) {
		binding, ok := action.SessionFromContext(ctx)
		if !ok {
			return useOutput{}, errors.New("missing binding")
		}
		_, found, err := action.LoadCheckpoint(ctx, "session_setup_intent")
		if err != nil {
			return useOutput{}, err
		}
		return useOutput{Owner: owner, ID: binding.ID, CreationTask: binding.Creation.TaskID, Isolated: !found}, nil
	}})
	if err != nil {
		return err
	}
	executors = append(executors, use)
	marker, err := action.NewFunc(action.FuncConfig[map[string]any, map[string]string]{Action: actions.Action{Name: "mark-owner"}, Run: func(ctx context.Context, _ map[string]any) (map[string]string, error) {
		return map[string]string{"owner": owner}, nil
	}})
	if err != nil {
		return err
	}
	executors = append(executors, marker)
	proposer, err := action.NewFunc(action.FuncConfig[useInput, map[string]string]{Action: actions.Action{Name: "propose-affinity"}, Run: func(ctx context.Context, _ useInput) (map[string]string, error) {
		return map[string]string{"owner": owner}, nil
	}, NextActions: func(ctx context.Context, input useInput, _ map[string]string) ([]action.NextAction, error) {
		raw, _ := json.Marshal(input)
		return []action.NextAction{{ActionName: "mark-owner", Input: json.RawMessage(`{}`)}, {ActionName: "use-resource", Input: raw}}, nil
	}})
	if err != nil {
		return err
	}
	if os.Getenv("FIXTURE_NO_PROPOSER") != "true" {
		executors = append(executors, proposer)
	}
	interval := 500 * time.Millisecond
	worker, err := agent.NewAgent(agent.AgentConfig{Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})), ID: owner, Type: "fixture", Actions: executors,
		Sessions: resources, ArtifactFileStore: files, HeartbeatInterval: &interval,
		Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "session-e2e"}})
	if err != nil {
		return err
	}
	err = worker.Serve(ctx, bus, nil)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

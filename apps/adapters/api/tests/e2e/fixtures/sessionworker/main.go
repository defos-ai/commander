// sessionworker is an executable E2E extension. Its HTTP peer controls when
// listener creation returns and when that resource enters public inventory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/artifacts"
	"github.com/defos-ai/commander/providers/fs/comms"
)

type resources struct {
	ctx     context.Context
	mu      sync.Mutex
	items   []sessions.Session
	changes chan struct{}
}

func (r *resources) Snapshot() []sessions.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sessions.CloneAll(r.items)
}
func (r *resources) Changes() <-chan struct{} { return r.changes }
func (r *resources) Control(_ context.Context, request sessions.Control) (sessions.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.items {
		if r.items[i].Reference == request.Reference {
			if request.Method == "close" {
				r.items[i].State = "closed"
				r.items[i].Revision++
				r.items[i].UpdatedAt = time.Now().UTC()
			}
			return r.items[i].Clone(), nil
		}
	}
	return sessions.Session{}, errors.New("unknown resource")
}

type fixtureAction struct {
	*action.Base
	owner, peer string
	resources   *resources
}

func (a *fixtureAction) Run(ctx context.Context, raw json.RawMessage) (action.Result, error) {
	if a.Action().Name == "propose-connections" {
		return action.Result{Output: json.RawMessage(`{}`), NextActions: []action.NextAction{
			{ActionName: "connect-resource", Input: raw},
			{ActionName: "connect-resource", Input: raw},
		}}, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.peer+"/create/"+a.owner, nil)
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
	var input struct {
		Scope string `json:"session_scope"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return action.Result{}, err
	}
	now := time.Now().UTC()
	resource := sessions.Session{Reference: sessions.Reference{ID: id.New(), ScopeID: input.Scope, OwnerAgentID: a.owner},
		Kind: "fixture", Label: "Owned E2E resource", State: "connected", Revision: 1,
		CreatedAt: now, UpdatedAt: now, VerifiedAt: &now}
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
		a.resources.items = append(a.resources.items, resource)
		a.resources.mu.Unlock()
		a.resources.changes <- struct{}{}
	}()
	return action.Result{Output: json.RawMessage(`{}`), Session: &resource}, nil
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	bus, err := comms.NewFsBus(os.Getenv("BUS_URL"))
	if err != nil {
		return err
	}
	defer bus.Close()
	files, err := artifacts.NewArtifactFileFsStore(os.Getenv("STORAGE_URL"))
	if err != nil {
		return err
	}
	owner := os.Getenv("FIXTURE_AGENT_ID")
	resources := &resources{ctx: ctx, changes: make(chan struct{}, 8)}
	var executors []action.Executor
	for _, name := range []string{"propose-connections", "connect-resource"} {
		mode := ""
		if name == "connect-resource" {
			mode = "create"
		}
		executors = append(executors, &fixtureAction{Base: action.NewBase(actions.Action{
			Name: name, Description: "E2E connection fixture", SessionMode: mode,
			InputSchema:  json.RawMessage(`{"type":"object","required":["session_scope"],"properties":{"session_scope":{"type":"string"}}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		}), owner: owner, peer: os.Getenv("FIXTURE_PEER"), resources: resources})
	}
	interval := 100 * time.Millisecond
	worker, err := agent.NewAgent(agent.AgentConfig{ID: owner, Type: "fixture", Actions: executors,
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

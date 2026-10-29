package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestAgentIDMatchesTypeWithOrWithoutRoleSuffix(t *testing.T) {
	for _, test := range []struct {
		name      string
		agentType agent.AgentType
		id        string
		prefix    string
		wantError bool
	}{
		{name: "plain type", agentType: "fixture", prefix: "fixture-agent-"},
		{name: "role suffix", agentType: "browser-agent", prefix: "browser-agent-"},
		{name: "explicit ID", agentType: "browser-agent", id: "browser-agent-12345678", prefix: "browser-agent-"},
		{name: "wrong type", agentType: "browser-agent", id: "other-agent-12345678", wantError: true},
		{name: "duplicated role", agentType: "browser-agent", id: "browser-agent-agent-12345678", wantError: true},
		{name: "malformed ID", agentType: "browser-agent", id: "browser-agent-INVALID", wantError: true},
		{name: "malformed type", agentType: "browser--agent", id: "browser-agent-12345678", wantError: true},
		{name: "uppercase type", agentType: "Browser-agent", wantError: true},
		{name: "empty type after suffix", agentType: "-agent", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, err := agent.NewAgent(agent.AgentConfig{ID: test.id, Type: test.agentType,
				Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"}, ArtifactFileStore: fakes.UnusedArtifacts{},
			})
			if test.wantError {
				if err == nil {
					t.Fatal("invalid agent type or ID was accepted")
				}
				if test.agentType == "browser-agent" && strings.Contains(err.Error(), "browser-agent-agent") {
					t.Fatalf("validation requested a duplicated role suffix: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !id.ValidAgent(a.ID()) || len(a.ID()) != len(test.prefix)+8 || !strings.HasPrefix(a.ID(), test.prefix) || a.Describe().Type != test.agentType {
				t.Fatalf("agent ID or public type changed: %+v", a.Describe())
			}
			if test.id != "" && a.ID() != test.id {
				t.Fatalf("explicit agent ID changed: %s", a.ID())
			}
		})
	}
}

func TestAgentSnapshotsRemainReadableDuringAnnouncementsAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	a := fixtures.StateAgent(t)
	bus := fakes.NewAnnouncementBus(ctx)
	scope := &environment.DiscoveryScope{Paths: []string{"/initial"}}

	for range 2 {
		serving, stop := context.WithCancel(ctx)
		finished := make(chan error, 1)
		go func() { finished <- a.Serve(serving, bus, scope) }()
		t.Cleanup(func() { stop() })
		started := fixtures.NextAnnouncement(t, ctx, bus)
		if started.Message.Type != controllerevents.AgentStarted {
			t.Fatalf("unexpected startup announcement: %s", started.Message.Type)
		}
		snapshot := readAgentState(t, ctx, a, agent.AgentReady)
		snapshot.DiscoveryScope.Paths[0] = "/changed-by-reader"
		if got := readAgentState(t, ctx, a, agent.AgentReady).DiscoveryScope.Paths[0]; got != "/initial" {
			t.Fatalf("reader changed the published discovery scope: %s", got)
		}
		deadline, expired := context.WithTimeout(ctx, 20*time.Millisecond)
		err := a.DiscoverActions(deadline, &environment.DiscoveryScope{Paths: []string{"/replacement"}})
		expired()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued transition did not respect cancellation: %v", err)
		}
		close(started.Release)
		if err := a.Serve(ctx, bus, nil); err == nil || !strings.Contains(err.Error(), "already serving") {
			t.Fatalf("concurrent Serve was not rejected: %v", err)
		}
		if err := a.DiscoverActions(ctx, nil); err == nil {
			t.Fatal("external discovery changed a serving agent")
		}

		stop()
		stopped := fixtures.NextAnnouncement(t, ctx, bus)
		if stopped.Message.Type != controllerevents.AgentStopped {
			t.Fatalf("unexpected shutdown announcement: %s", stopped.Message.Type)
		}
		readAgentState(t, ctx, a, agent.AgentStopped)
		deadline, expired = context.WithTimeout(ctx, 20*time.Millisecond)
		err = a.Serve(deadline, bus, nil)
		expired()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("restart overtook the shutdown announcement: %v", err)
		}
		close(stopped.Release)
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("unexpected shutdown result: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("agent did not stop")
		}
	}
}

func TestAgentRejectsCompetingWorkAndDiscovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := make(chan struct{}, 1)
	executor, err := action.NewFunc(action.FuncConfig[map[string]any, map[string]any]{
		Action: actions.Action{Name: "inspect"},
		Run: func(ctx context.Context, _ map[string]any) (map[string]any, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := fixtures.StateAgent(t, executor)
	if err := a.DiscoverActions(ctx, nil); err != nil {
		t.Fatal(err)
	}
	working, stop := context.WithCancel(ctx)
	defer stop()
	finished := make(chan error, 1)
	call := agent.ActionCall{ActionName: "inspect", Work: messages.WorkContext{OperationID: id.New(), TaskID: id.New()}}
	go func() { _, err := a.Work(working, call); finished <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("action did not start")
	}

	snapshot := readAgentState(t, ctx, a, agent.AgentWorking)
	if snapshot.Work == nil || snapshot.Work.OperationID != call.Work.OperationID || snapshot.Work.TaskID != call.Work.TaskID ||
		snapshot.Work.ActionName != call.ActionName || snapshot.Work.CommandID != "" {
		t.Fatalf("current work did not identify the direct action: %+v", snapshot.Work)
	}
	if _, err := a.Work(ctx, call); err == nil {
		t.Fatal("agent accepted concurrent work")
	}
	if err := a.DiscoverActions(ctx, nil); err == nil {
		t.Fatal("agent allowed discovery during work")
	}
	if err := a.Serve(ctx, fakes.NewResultBus(), nil); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("agent allowed Serve during work: %v", err)
	}
	stop()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected action cancellation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled action did not return")
	}
	if snapshot := readAgentState(t, ctx, a, agent.AgentReady); snapshot.Work != nil {
		t.Fatalf("cancelled work remained in snapshot: %+v", snapshot.Work)
	}
}

func readAgentState(t *testing.T, ctx context.Context, a *agent.Agent, expected agent.AgentStatus) agent.AgentSnapshot {
	t.Helper()
	read := make(chan agent.AgentSnapshot, 1)
	go func() {
		if status := a.Status(); status != expected {
			t.Errorf("status=%s, want %s", status, expected)
		}
		read <- a.Describe()
	}()
	select {
	case snapshot := <-read:
		if snapshot.Status != expected {
			t.Fatalf("snapshot status=%s, want %s", snapshot.Status, expected)
		}
		return snapshot
	case <-ctx.Done():
		t.Fatal("reading agent state blocked")
		return agent.AgentSnapshot{}
	}
}

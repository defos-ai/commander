package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"commander/framework/agent"
	"commander/framework/agent/action"
	"commander/framework/agent/environment"
	"commander/framework/comms"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	"commander/framework/domain/actions"
	"commander/framework/domain/artifacts"
	"commander/framework/id"
)

func TestAgentSnapshotsRemainReadableDuringAnnouncementsAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	a := stateAgent(t)
	bus := &announcementBus{ctx: ctx, published: make(chan announcement, 4)}
	scope := &environment.DiscoveryScope{Paths: []string{"/initial"}}

	for range 2 {
		serving, stop := context.WithCancel(ctx)
		finished := make(chan error, 1)
		go func() { finished <- a.Serve(serving, bus, scope) }()
		t.Cleanup(func() { stop() })
		started := nextAnnouncement(t, ctx, bus)
		if started.message.Type != controllerevents.AgentStarted {
			t.Fatalf("unexpected startup announcement: %s", started.message.Type)
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
		close(started.release)
		if err := a.Serve(ctx, bus, nil); err == nil || !strings.Contains(err.Error(), "already serving") {
			t.Fatalf("concurrent Serve was not rejected: %v", err)
		}
		if err := a.DiscoverActions(ctx, nil); err == nil {
			t.Fatal("external discovery changed a serving agent")
		}

		stop()
		stopped := nextAnnouncement(t, ctx, bus)
		if stopped.message.Type != controllerevents.AgentStopped {
			t.Fatalf("unexpected shutdown announcement: %s", stopped.message.Type)
		}
		readAgentState(t, ctx, a, agent.AgentStopped)
		deadline, expired = context.WithTimeout(ctx, 20*time.Millisecond)
		err = a.Serve(deadline, bus, nil)
		expired()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("restart overtook the shutdown announcement: %v", err)
		}
		close(stopped.release)
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
	a := stateAgent(t, executor)
	if err := a.DiscoverActions(ctx, nil); err != nil {
		t.Fatal(err)
	}
	working, stop := context.WithCancel(ctx)
	defer stop()
	finished := make(chan error, 1)
	call := agent.ActionCall{ActionName: "inspect", Work: messages.WorkContext{OperationID: id.New(), TaskID: id.New(), StepID: "inspect-page"}}
	go func() { _, err := a.Work(working, call); finished <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("action did not start")
	}

	snapshot := readAgentState(t, ctx, a, agent.AgentWorking)
	if snapshot.CurrentWork == nil || snapshot.CurrentWork.OperationID != call.Work.OperationID || snapshot.CurrentWork.TaskID != call.Work.TaskID ||
		snapshot.CurrentWork.StepID != call.Work.StepID || snapshot.CurrentWork.ActionName != call.ActionName || snapshot.CurrentWork.CommandID != "" {
		t.Fatalf("current work did not identify the direct action: %+v", snapshot.CurrentWork)
	}
	if _, err := a.Work(ctx, call); err == nil {
		t.Fatal("agent accepted concurrent work")
	}
	if err := a.DiscoverActions(ctx, nil); err == nil {
		t.Fatal("agent allowed discovery during work")
	}
	if err := a.Serve(ctx, &resultBus{}, nil); err == nil || !strings.Contains(err.Error(), "busy") {
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
	if snapshot := readAgentState(t, ctx, a, agent.AgentReady); snapshot.CurrentWork != nil {
		t.Fatalf("cancelled work remained in snapshot: %+v", snapshot.CurrentWork)
	}
}

func stateAgent(t *testing.T, actions ...action.Executor) *agent.Agent {
	t.Helper()
	noHeartbeat := time.Duration(0)
	a, err := agent.NewAgent(agent.AgentConfig{Type: "fixture", Actions: actions,
		Server:            agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "fixture"},
		HeartbeatInterval: &noHeartbeat, ArtifactFileStore: unusedArtifacts{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
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

type announcement struct {
	message messages.Message
	release chan struct{}
}

type announcementBus struct {
	ctx       context.Context
	published chan announcement
}

func (b *announcementBus) Publish(ctx context.Context, _ string, message messages.Message) error {
	publication := announcement{message: message, release: make(chan struct{})}
	select {
	case b.published <- publication:
	case <-b.ctx.Done():
		return b.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-publication.release:
		return nil
	case <-b.ctx.Done():
		return b.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*announcementBus) Subscribe(context.Context, string) (comms.Subscription, error) {
	return idleSubscription{}, nil
}
func (*announcementBus) Close() error { return nil }

type idleSubscription struct{}

func (idleSubscription) Next(ctx context.Context) (messages.Message, error) {
	<-ctx.Done()
	return messages.Message{}, ctx.Err()
}
func (idleSubscription) Ack(context.Context, string) error { return nil }
func (idleSubscription) Close() error                      { return nil }

func nextAnnouncement(t *testing.T, ctx context.Context, bus *announcementBus) announcement {
	t.Helper()
	select {
	case publication := <-bus.published:
		return publication
	case <-ctx.Done():
		t.Fatal("expected agent announcement")
		return announcement{}
	}
}

type unusedArtifacts struct{}

func (unusedArtifacts) Put(context.Context, string, artifacts.FileSource) (artifacts.ArtifactFile, error) {
	return artifacts.ArtifactFile{}, errors.New("unexpected artifact write")
}
func (unusedArtifacts) Get(context.Context, string, artifacts.ArtifactFile) ([]byte, error) {
	return nil, errors.New("unexpected artifact read")
}

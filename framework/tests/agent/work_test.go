package agent_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"commander/framework/agent"
	"commander/framework/agent/action"
	"commander/framework/comms"
	agentcommands "commander/framework/comms/clients/agent/commands"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/id"
)

func TestAgentAnnouncesWorkWithoutHeartbeat(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			executor, err := action.NewFunc(action.FuncConfig[map[string]any, map[string]any]{
				Action: actions.Action{Name: "inspect"},
				Run: func(ctx context.Context, _ map[string]any) (map[string]any, error) {
					close(started)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-release:
					}
					if fail {
						return nil, errors.New("action failed")
					}
					return map[string]any{"observed": true}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			a := stateAgent(t, executor)
			bus := &workBus{agentID: a.ID(), resultBus: resultBus{results: make(chan messages.Message, 16)}, commands: make(chan messages.Message, 1)}
			serving, stop := context.WithCancel(ctx)
			defer stop()
			finished := make(chan error, 1)
			go func() { finished <- a.Serve(serving, bus, nil) }()
			next := func(kind messages.MessageType) messages.Message {
				t.Helper()
				select {
				case message := <-bus.results:
					if message.Type != kind {
						t.Fatalf("event type=%s, want %s", message.Type, kind)
					}
					return message
				case <-ctx.Done():
					t.Fatal("agent did not publish state or result")
					return messages.Message{}
				}
			}
			next(controllerevents.AgentStarted)
			call := agent.ActionCall{ActionName: "inspect", Input: []byte(`{"secret":"private-input"}`),
				Work: messages.WorkContext{OperationID: id.New(), TaskID: id.New(), StepID: "inspect-page"}}
			command, err := agentcommands.NewRunAction(a.ID(), call)
			if err != nil {
				t.Fatal(err)
			}
			bus.commands <- command
			working := next(controllerevents.AgentUpdated)
			var snapshot agent.AgentSnapshot
			if err := agent.Decode(working.Payload, &snapshot); err != nil {
				t.Fatal(err)
			}
			want := agent.Work{OperationID: call.Work.OperationID, TaskID: call.Work.TaskID, StepID: call.Work.StepID,
				ActionName: call.ActionName, CommandID: command.ID}
			if snapshot.Status != agent.AgentWorking || snapshot.Work == nil || *snapshot.Work != want ||
				working.CorrelationID != "" || bytes.Contains(working.Payload, []byte("private-input")) {
				t.Fatalf("working announcement lost work identity or exposed input: %+v", snapshot)
			}
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("action did not start")
			}
			description := readAgentState(t, ctx, a, agent.AgentWorking)
			if description.Work == nil || *description.Work != want {
				t.Fatalf("runtime did not expose current work: %+v", description)
			}
			description.Work.TaskID = "changed-by-reader"
			if *a.Describe().Work != want {
				t.Fatal("reader changed the runtime's current work")
			}
			record := agents.Agent{AgentSnapshot: snapshot}
			copy := record.Clone()
			copy.Work.TaskID = "changed-by-store-reader"
			if *record.Work != want {
				t.Fatal("inventory clone shared current work")
			}

			close(release)
			ready := next(controllerevents.AgentUpdated)
			snapshot = agent.AgentSnapshot{}
			if err := agent.Decode(ready.Payload, &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.Status != agent.AgentReady || snapshot.Work != nil || ready.CorrelationID != "" {
				t.Fatalf("finished work remained in announcement: %+v", snapshot)
			}
			resultKind := controllerevents.ActionSucceeded
			if fail {
				resultKind = controllerevents.CommandFailed
			}
			result := next(resultKind)
			if result.CorrelationID != command.ID || result.Work != call.Work {
				t.Fatalf("work announcements changed command result identity: %+v", result)
			}
			if a.Describe().Work != nil {
				t.Fatal("finished work remained in runtime snapshot")
			}
			stop()
			next(controllerevents.AgentStopped)
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("unexpected shutdown result: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("agent did not stop")
			}
		})
	}
}

type workBus struct {
	resultBus
	agentID  string
	commands chan messages.Message
}

func (b *workBus) Subscribe(_ context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	if inbox.Name != b.agentID || inbox.Mode != comms.TransientExclusive {
		return nil, errors.New("agent inbox must be transient and owned by its process ID")
	}
	switch inbox.Topic {
	case "agents." + b.agentID + ".commands":
		return workSubscription{commands: b.commands}, nil
	case "agents." + b.agentID + ".events":
		return idleSubscription{}, nil
	default:
		return nil, errors.New("unexpected agent destination: " + inbox.Topic)
	}
}

type workSubscription struct {
	idleSubscription
	commands <-chan messages.Message
}

func (s workSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	select {
	case message := <-s.commands:
		return testDelivery{message: message}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type testDelivery struct {
	message messages.Message
}

func (d testDelivery) Message() messages.Message  { return d.message }
func (testDelivery) Ack(context.Context) error    { return nil }
func (testDelivery) Reject(context.Context) error { return nil }

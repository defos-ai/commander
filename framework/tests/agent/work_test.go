package agent_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
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
			a := fixtures.StateAgent(t, executor)
			bus := fakes.NewWorkBus(a.ID())
			serving, stop := context.WithCancel(ctx)
			defer stop()
			finished := make(chan error, 1)
			go func() { finished <- a.Serve(serving, bus, nil) }()
			next := func(kind messages.MessageType) messages.Message {
				t.Helper()
				select {
				case message := <-bus.Results():
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
				Work: messages.WorkContext{OperationID: id.New(), TaskID: id.New()}}
			command, err := agentcommands.NewRunAction(a.ID(), call)
			if err != nil {
				t.Fatal(err)
			}
			if err := bus.SendCommand(ctx, command); err != nil {
				t.Fatal(err)
			}
			working := next(controllerevents.AgentUpdated)
			var snapshot agent.AgentSnapshot
			if err := agent.Decode(working.Payload, &snapshot); err != nil {
				t.Fatal(err)
			}
			want := agent.Work{OperationID: call.Work.OperationID, TaskID: call.Work.TaskID, ActionName: call.ActionName, CommandID: command.ID}
			if snapshot.Status != agent.AgentWorking || !reflect.DeepEqual(snapshot.Work, &want) ||
				working.CorrelationID != "" || bytes.Contains(working.Payload, []byte("private-input")) {
				t.Fatalf("working announcement lost work identity or exposed input: %+v", snapshot)
			}
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("action did not start")
			}
			description := readAgentState(t, ctx, a, agent.AgentWorking)
			if !reflect.DeepEqual(description.Work, &want) {
				t.Fatalf("runtime did not expose current work: %+v", description)
			}
			description.Work.TaskID = "changed-by-reader"
			if !reflect.DeepEqual(a.Describe().Work, &want) {
				t.Fatal("reader changed the runtime's current work")
			}
			record := agents.Agent{AgentSnapshot: snapshot}
			copy := record.Clone()
			copy.Work.TaskID = "changed-by-store-reader"
			if !reflect.DeepEqual(record.Work, &want) {
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

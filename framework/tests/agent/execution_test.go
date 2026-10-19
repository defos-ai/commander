package agent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

func TestAgentWorkSuppliesCurrentExecutionIdentity(t *testing.T) {
	observed := make(chan action.Execution, 2)
	executor := executionProbe(t, observed)
	runtime := stateAgent(t, executor)
	if err := runtime.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := action.ExecutionFromContext(t.Context()); ok {
		t.Fatal("unrelated context contains execution identity")
	}
	// An embedding caller's identity must not leak into a later dispatch.
	outer := action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID(),
		Work: messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}})
	for _, work := range []messages.WorkContext{
		{OperationID: id.OperationID(), TaskID: id.TaskID(), StepID: "provision"},
		{},
	} {
		_, err := runtime.Work(outer, agent.ActionCall{ActionName: "inspect-execution", Work: work})
		if err != nil {
			t.Fatal(err)
		}
		if actual := <-observed; actual != (action.Execution{Work: work}) {
			t.Fatalf("action received stale or incorrect work identity: %+v", actual)
		}
	}
}

func TestAgentServeSuppliesCommandAndWorkIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	observed := make(chan action.Execution, 1)
	runtime := stateAgent(t, executionProbe(t, observed))
	bus := &workBus{agentID: runtime.ID(), resultBus: resultBus{results: make(chan messages.Message, 16)},
		commands: make(chan messages.Message, 1)}
	serving, stop := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() { finished <- runtime.Serve(serving, bus, nil) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("agent shutdown failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not shut down")
		}
	})
	select {
	case message := <-bus.results:
		if message.Type != controllerevents.AgentStarted {
			t.Fatalf("unexpected startup event: %s", message.Type)
		}
	case <-ctx.Done():
		t.Fatal("agent did not register")
	}
	call := agent.ActionCall{ActionName: "inspect-execution", Work: messages.WorkContext{
		OperationID: id.OperationID(), TaskID: id.TaskID(), StepID: "provision"}}
	command, err := agentcommands.NewRunAction(runtime.ID(), call)
	if err != nil {
		t.Fatal(err)
	}
	bus.commands <- command
	select {
	case actual := <-observed:
		if actual != (action.Execution{CommandID: command.ID, Work: call.Work}) {
			t.Fatalf("action lost dispatch identity: %+v", actual)
		}
	case <-ctx.Done():
		t.Fatal("action did not execute")
	}
	for {
		select {
		case message := <-bus.results:
			if message.Type == controllerevents.ActionSucceeded {
				if message.CorrelationID != command.ID || message.Work != call.Work {
					t.Fatal("execution context changed the result's dispatch identity")
				}
				stop()
				return
			}
		case <-ctx.Done():
			t.Fatal("action did not publish a result")
		}
	}
}

func executionProbe(t *testing.T, observed chan<- action.Execution) action.Executor {
	t.Helper()
	executor, err := action.NewFunc(action.FuncConfig[struct{}, struct{}]{
		Action: actions.Action{Name: "inspect-execution"},
		Run: func(ctx context.Context, _ struct{}) (struct{}, error) {
			execution, ok := action.ExecutionFromContext(ctx)
			if !ok {
				return struct{}{}, errors.New("action has no execution identity")
			}
			observed <- execution
			return struct{}{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

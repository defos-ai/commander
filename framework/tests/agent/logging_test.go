package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/defos-ai/commander/framework/agent/action"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestAgentLogsBindTheDispatchedExecution(t *testing.T) {
	for _, work := range []messages.WorkContext{
		{OperationID: id.OperationID(), TaskID: id.TaskID()},
		{},
	} {
		name := "standalone"
		if work.OperationID != "" {
			name = "operation"
		}
		t.Run(name, func(t *testing.T) {
			const text = "  Compiling <module>…\nTests are next  "
			inherited := fakes.NewLogProvider(errors.New("inherited provider must not run"))
			parent := action.WithLogs(t.Context(), inherited)
			executor := fixtures.LoggingAction(t, func(ctx context.Context) error {
				ctx = action.WithExecution(context.WithoutCancel(ctx), action.Execution{CommandID: id.MessageID(),
					Work: messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}})
				if err := action.Log(ctx, text); err != nil {
					return err
				}
				return action.Log(ctx, text)
			})
			fixture := fixtures.StartLoggingAgent(t, parent, executor, fakes.LogBusConfig{})

			command := fixture.Command(t, work)
			first, second := fixture.Attempt(t), fixture.Attempt(t)
			outcome := fixture.Outcome(t, command.ID)

			if outcome.Type != controllerevents.ActionSucceeded || len(inherited.Calls()) != 0 {
				t.Fatalf("dispatched logging inherited another provider or failed: %s", outcome.Payload)
			}
			if first.Message.ID == second.Message.ID {
				t.Fatal("separate log calls reused an observation ID")
			}
			for _, publication := range []fakes.LogAttempt{first, second} {
				log, err := controllerevents.DecodeExecutionLog(publication.Message)
				if err != nil {
					t.Fatal(err)
				}
				if publication.Topic != messages.ControllerEvents(messages.DefaultControllerType) ||
					log.AgentID != fixture.Runtime.ID() || log.CommandID != command.ID || log.Work != work ||
					log.ActionName != fixtures.LoggingActionName || log.Message != text || log.CreatedAt.IsZero() ||
					!publication.Message.ExpiresAt.IsZero() {
					t.Fatalf("log lost its original dispatch identity or text: %+v", log)
				}
			}
		})
	}
}

func TestAgentLogPublicationEndsWhenItsActionReturns(t *testing.T) {
	finished := make(chan error, 1)
	releaseAction := make(chan struct{})
	executor := fixtures.LoggingAction(t, func(ctx context.Context) error {
		go func() { finished <- action.Log(context.WithoutCancel(ctx), "Publication started before returning") }()
		select {
		case <-releaseAction:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	fixture := fixtures.StartLoggingAgent(t, t.Context(), executor, fakes.LogBusConfig{Block: true})
	command := fixture.Command(t, messages.WorkContext{})
	fixture.Attempt(t)

	close(releaseAction)
	err := <-finished
	outcome := fixture.Outcome(t, command.ID)

	if !errors.Is(err, context.Canceled) || outcome.Type != controllerevents.ActionSucceeded {
		t.Fatalf("detached publication outlived its action: error=%v outcome=%s", err, outcome.Type)
	}
}

func TestAgentLogPublicationEndsOnCallerCancellation(t *testing.T) {
	finished := make(chan error, 1)
	cancelCall := make(chan context.CancelFunc, 1)
	executor := fixtures.LoggingAction(t, func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		defer cancel()
		cancelCall <- cancel
		finished <- action.Log(ctx, "Transport is blocked")
		return nil
	})
	fixture := fixtures.StartLoggingAgent(t, t.Context(), executor, fakes.LogBusConfig{Block: true})
	command := fixture.Command(t, messages.WorkContext{})
	fixture.Attempt(t)
	cancel := <-cancelCall

	cancel()
	err := <-finished
	outcome := fixture.Outcome(t, command.ID)

	if !errors.Is(err, context.Canceled) || outcome.Type != controllerevents.ActionSucceeded {
		t.Fatalf("caller cancellation did not release logging: %v outcome=%s", err, outcome.Type)
	}
}

func TestAgentLogPublicationEndsOnServiceCancellation(t *testing.T) {
	finished := make(chan error, 1)
	retained := make(chan context.Context, 1)
	executor := fixtures.LoggingAction(t, func(ctx context.Context) error {
		ctx = context.WithoutCancel(ctx)
		retained <- ctx
		finished <- action.Log(ctx, "Transport is blocked")
		return nil
	})
	fixture := fixtures.StartLoggingAgent(t, t.Context(), executor, fakes.LogBusConfig{Block: true})
	fixture.Command(t, messages.WorkContext{})
	fixture.Attempt(t)
	scope := <-retained

	fixture.Cancel()
	err := <-finished
	fixture.Stop(t)
	lateErr := action.Log(scope, "After service cancellation")

	if !errors.Is(err, context.Canceled) || !errors.Is(lateErr, context.Canceled) {
		t.Fatalf("detached logging outlived the serving instance: active=%v late=%v", err, lateErr)
	}
	select {
	case publication := <-fixture.Bus.Attempts():
		t.Fatalf("canceled service published another log: %+v", publication.Message)
	default:
	}
}

func TestAgentLogPublicationFailureIsNotRetried(t *testing.T) {
	want := errors.New("log publication outcome unknown")
	finished := make(chan error, 1)
	executor := fixtures.LoggingAction(t, func(ctx context.Context) error {
		finished <- action.Log(ctx, "Build started")
		return nil
	})
	fixture := fixtures.StartLoggingAgent(t, t.Context(), executor, fakes.LogBusConfig{Failure: want})

	command := fixture.Command(t, messages.WorkContext{})
	fixture.Attempt(t)
	err := <-finished
	outcome := fixture.Outcome(t, command.ID)

	if err != want || outcome.Type != controllerevents.ActionSucceeded {
		t.Fatalf("publication failure was replaced or forced action failure: error=%v outcome=%s", err, outcome.Type)
	}
	select {
	case publication := <-fixture.Bus.Attempts():
		t.Fatalf("logging retried an uncertain publication: %+v", publication.Message)
	default:
	}
}

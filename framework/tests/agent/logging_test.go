package agent_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/defos-ai/commander/framework/agent"
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

func TestAgentStandaloneWorkRemovesInheritedLogAccess(t *testing.T) {
	provider := fakes.NewLogProvider(nil)
	observed := make(chan error, 1)
	executor := fixtures.LoggingAction(t, func(ctx context.Context) error {
		observed <- action.Log(ctx, "Standalone work")
		return nil
	})
	runtime := fixtures.StateAgent(t, executor)
	if err := runtime.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	ctx := action.WithLogs(t.Context(), provider)
	ctx = action.WithExecution(ctx, action.Execution{CommandID: id.MessageID()})

	_, err := runtime.Work(ctx, agent.ActionCall{ActionName: fixtures.LoggingActionName})
	logErr := <-observed

	if err != nil || !errors.Is(logErr, action.ErrLogsUnavailable) || len(provider.Calls()) != 0 {
		t.Fatalf("standalone work retained inherited logging access: work=%v log=%v", err, logErr)
	}
}

func TestAgentLogScopeEndsWithTheAction(t *testing.T) {
	retained := make(chan context.Context, 1)
	executor := fixtures.LoggingAction(t, func(ctx context.Context) error {
		retained <- context.WithoutCancel(ctx)
		return nil
	})
	fixture := fixtures.StartLoggingAgent(t, t.Context(), executor, fakes.LogBusConfig{})
	command := fixture.Command(t, messages.WorkContext{})
	scope := <-retained
	if outcome := fixture.Outcome(t, command.ID); outcome.Type != controllerevents.ActionSucceeded {
		t.Fatalf("action failed before its scope ended: %s", outcome.Payload)
	}

	err := action.Log(scope, "Too late")
	fixture.Stop(t)
	serviceErr := action.Log(scope, "After serving stopped")

	if !errors.Is(err, context.Canceled) || !errors.Is(serviceErr, context.Canceled) {
		t.Fatalf("retained scope survived its action or service: action=%v service=%v", err, serviceErr)
	}
	select {
	case publication := <-fixture.Bus.Attempts():
		t.Fatalf("ended execution published a log: %+v", publication.Message)
	default:
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

func TestAgentLogPublicationUsesCallerAndTransportDeadlines(t *testing.T) {
	for _, timeout := range []time.Duration{0, 20 * time.Millisecond} {
		t.Run(timeout.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				finished := make(chan error, 1)
				executor := fixtures.LoggingAction(t, func(ctx context.Context) error {
					ctx = context.WithoutCancel(ctx)
					if timeout != 0 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, timeout)
						defer cancel()
					}
					finished <- action.Log(ctx, "Transport is blocked")
					return nil
				})
				fixture := fixtures.StartLoggingAgent(t, t.Context(), executor, fakes.LogBusConfig{Block: true})
				command := fixture.Command(t, messages.WorkContext{})
				started := time.Now()

				publication := fixture.Attempt(t)
				err := <-finished
				outcome := fixture.Outcome(t, command.ID)

				want := 5 * time.Second
				if timeout != 0 {
					want = timeout
				}
				deadline, ok := publication.Context.Deadline()
				if !ok || !deadline.Equal(started.Add(want)) || time.Since(started) != want ||
					!errors.Is(err, context.DeadlineExceeded) || outcome.Type != controllerevents.ActionSucceeded {
					t.Fatalf("log publication ignored its bounded deadline: deadline=%v elapsed=%s error=%v outcome=%s", deadline, time.Since(started), err, outcome.Type)
				}
			})
		})
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

func TestAgentCanLogAfterItsAdmissionDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		executor := fixtures.LoggingAction(t, func(ctx context.Context) error {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			return action.Log(ctx, "Still building after admission expired")
		})
		fixture := fixtures.StartLoggingAgent(t, t.Context(), executor, fakes.LogBusConfig{})
		command := fixture.Command(t, messages.WorkContext{}, time.Now().Add(time.Second))
		<-started

		time.Sleep(2 * time.Second)
		close(release)
		publication := fixture.Attempt(t)
		outcome := fixture.Outcome(t, command.ID)

		if outcome.Type != controllerevents.ActionSucceeded || !publication.Message.ExpiresAt.IsZero() {
			t.Fatalf("admission expiry canceled logging during active work: outcome=%s log=%+v", outcome.Type, publication.Message)
		}
	})
}

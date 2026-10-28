package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

func TestLogRequiresAnInjectedProvider(t *testing.T) {
	for _, ctx := range []context.Context{
		t.Context(),
		action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID(),
			Work: messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}}),
	} {
		err := action.Log(ctx, "Build started")

		if !errors.Is(err, action.ErrLogsUnavailable) {
			t.Fatalf("execution metadata granted logging access: %v", err)
		}
	}
}

func TestLogNilProviderShadowsInheritedAccess(t *testing.T) {
	provider := fakes.NewLogProvider(nil)
	ctx := action.WithLogs(action.WithLogs(t.Context(), provider), nil)

	err := action.Log(ctx, "Build started")

	if !errors.Is(err, action.ErrLogsUnavailable) || len(provider.Calls()) != 0 {
		t.Fatalf("nil provider retained inherited logging access: %v", err)
	}
}

func TestLogRejectsInvalidMessagesBeforeCallingProvider(t *testing.T) {
	for _, scenario := range []struct {
		name, message string
	}{
		{name: "empty"},
		{name: "whitespace", message: " \t\n"},
		{name: "invalid UTF-8", message: "Build\xff"},
		{name: "too many ASCII bytes", message: strings.Repeat("a", executions.MaxLogMessageSize+1)},
		{name: "too many UTF-8 bytes", message: strings.Repeat("é", executions.MaxLogMessageSize/2) + "a"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			provider := fakes.NewLogProvider(nil)
			ctx := action.WithLogs(t.Context(), provider)

			err := action.Log(ctx, scenario.message)

			if err == nil || len(provider.Calls()) != 0 {
				t.Fatalf("invalid message reached provider: %v", err)
			}
		})
	}
}

func TestLogForwardsExactMessageAndContext(t *testing.T) {
	for _, scenario := range []struct {
		name, message string
	}{
		{name: "unmodified text", message: "  Building agent…\nStep 1 of 2  "},
		{name: "ASCII byte boundary", message: strings.Repeat("a", executions.MaxLogMessageSize)},
		{name: "UTF-8 byte boundary", message: strings.Repeat("é", executions.MaxLogMessageSize/2)},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			provider := fakes.NewLogProvider(nil)
			ctx := action.WithLogs(t.Context(), provider)

			err := action.Log(ctx, scenario.message)

			if err != nil {
				t.Fatal(err)
			}
			calls := provider.Calls()
			if len(calls) != 1 || calls[0].Context != ctx || calls[0].Message != scenario.message {
				t.Fatal("logging changed the caller context or message")
			}
		})
	}
}

func TestLogReturnsProviderErrors(t *testing.T) {
	want := errors.New("log storage unavailable")
	provider := fakes.NewLogProvider(want)
	ctx := action.WithLogs(t.Context(), provider)

	err := action.Log(ctx, "Build started")

	if err != want || len(provider.Calls()) != 1 {
		t.Fatalf("provider error was replaced: %v", err)
	}
}

func TestLogHonorsCancellationBeforeValidationAndProvider(t *testing.T) {
	provider := fakes.NewLogProvider(nil)
	ctx, cancel := context.WithCancel(t.Context())
	ctx = action.WithLogs(ctx, provider)
	cancel()

	err := action.Log(ctx, "")

	if !errors.Is(err, context.Canceled) || len(provider.Calls()) != 0 {
		t.Fatalf("canceled logging reached validation or provider: %v", err)
	}
}

func TestLogChecksCancellationAfterProviderSuccess(t *testing.T) {
	provider := fakes.NewLogProvider(nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	provider.CancelAfterLog(cancel)
	ctx = action.WithLogs(ctx, provider)

	err := action.Log(ctx, "Build started")

	if !errors.Is(err, context.Canceled) || len(provider.Calls()) != 1 {
		t.Fatalf("logging ignored cancellation during provider call: %v", err)
	}
}

func TestLogPreservesProviderErrorWhenCallIsCanceled(t *testing.T) {
	want := errors.New("log write outcome uncertain")
	provider := fakes.NewLogProvider(want)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	provider.CancelAfterLog(cancel)
	ctx = action.WithLogs(ctx, provider)

	err := action.Log(ctx, "Build started")

	if err != want || len(provider.Calls()) != 1 {
		t.Fatalf("cancellation replaced the provider error: %v", err)
	}
}

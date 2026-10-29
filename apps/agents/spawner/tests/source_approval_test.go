package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func TestSpawnAgentWaitsForInstallationQuestionBeforeSecretAccessOrLaunch(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	execution := fixtures.SourceApprovalExecution()
	questions := fakes.NewSourceQuestions()
	ctx = action.WithQuestions(action.WithExecution(ctx, execution), questions)
	secretRequests := make(chan string, 1)
	ctx = action.WithSecrets(ctx, fakes.SecretRequester(func(_ context.Context, name string) ([]byte, error) {
		secretRequests <- name
		return []byte("private-installation-test-token"), nil
	}))
	launches := make(chan fakes.SpawnInvocation, 1)
	executor := fixtures.SpawnAction(t, fakes.LauncherFunc(func(_ context.Context, requestID, reference string) (runtimes.Result, error) {
		launches <- fakes.SpawnInvocation{RequestID: requestID, Reference: reference}
		return fixtures.SpawnResult(), nil
	}))
	finished := make(chan error, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("spawn did not stop after question cancellation")
		}
	})

	go func() {
		defer close(done)
		_, err := executor.Run(ctx, json.RawMessage(`{"package":"https://github.com/other/worker-agent","visibility":"private"}`))
		finished <- err
	}()
	request := receiveSourceQuestion(t, ctx, questions)
	if request.Key != "install-source" || request.Text != fixtures.PrivateSourceApprovalQuestion || !reflect.DeepEqual(request.Options, []string{"Yes", "No"}) {
		t.Fatalf("installation question did not identify its canonical source and visibility: %+v", request)
	}
	select {
	case <-secretRequests:
		t.Fatal("private token requested before the installation answer")
	case <-launches:
		t.Fatal("agent launched before the installation answer")
	case err := <-finished:
		t.Fatalf("spawn stopped while waiting for its answer: %v", err)
	default:
	}
	answer := fixtures.SourceApprovalAnswer("Yes", operations.AnswerOperator)
	answer.Text = fixtures.PrivateSourceApprovalQuestion
	questions.Answer(answer, nil)

	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("spawn did not resume after installation approval")
	}
	select {
	case name := <-secretRequests:
		if name != "GITHUB_TOKEN" {
			t.Fatalf("approved install requested an unexpected secret: %s", name)
		}
	default:
		t.Fatal("approved install did not request its private token")
	}
	select {
	case call := <-launches:
		if call != (fakes.SpawnInvocation{RequestID: "command:" + execution.CommandID, Reference: "https://github.com/other/worker-agent"}) {
			t.Fatalf("approved install lost its execution or source: %+v", call)
		}
	default:
		t.Fatal("approved install did not launch its agent")
	}
}

func TestSpawnAgentCancellationStopsPendingInstallationQuestion(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	questions := fakes.NewSourceQuestions()
	ctx = action.WithQuestions(action.WithExecution(ctx, fixtures.SourceApprovalExecution()), questions)
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: fixtures.SpawnResult()})
	executor := fixtures.SpawnAction(t, launcher)
	finished := make(chan error, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("spawn did not stop after question cancellation")
		}
	})

	go func() {
		defer close(done)
		_, err := executor.Run(ctx, json.RawMessage(`{"package":"github.com/other/worker-agent@stable"}`))
		finished <- err
	}()
	receiveSourceQuestion(t, ctx, questions)
	cancel()

	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, packages.ErrSourceApprovalRequired) || len(launcher.Calls()) != 0 {
			t.Fatalf("cancelled question did not end the wait before launch: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled spawn kept waiting for installation approval")
	}
}

func TestSpawnAgentAcceptsOperatorOrModelInstallationAnswer(t *testing.T) {
	for _, origin := range []operations.AnswerOrigin{operations.AnswerOperator, operations.AnswerModel} {
		t.Run(string(origin), func(t *testing.T) {
			questions := fakes.NewSourceQuestions()
			questions.Answer(fixtures.SourceApprovalAnswer("Yes", origin), nil)
			execution := fixtures.SourceApprovalExecution()
			ctx := action.WithQuestions(action.WithExecution(t.Context(), execution), questions)
			launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: fixtures.SpawnResult()})
			executor := fixtures.SpawnAction(t, launcher)

			_, err := executor.Run(ctx, json.RawMessage(`{"package":"github.com/other/worker-agent@stable"}`))

			if err != nil {
				t.Fatal(err)
			}
			if calls := launcher.Calls(); len(calls) != 1 || calls[0] != (fakes.SpawnInvocation{RequestID: "command:" + execution.CommandID, Reference: fixtures.UntrustedSource}) {
				t.Fatalf("accepted answer did not launch its source: %+v", calls)
			}
			request := receiveSourceQuestion(t, t.Context(), questions)
			if request.Key != "install-source" || request.Text != fixtures.SourceApprovalQuestion {
				t.Fatalf("question did not use its stable execution key or canonical source: %+v", request)
			}
		})
	}
}

func TestTrustedSourceDoesNotAskInstallationQuestion(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	questions := fakes.NewSourceQuestions()
	ctx = action.WithQuestions(action.WithExecution(ctx, fixtures.SourceApprovalExecution()), questions)
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: fixtures.SpawnResult()})
	executor := fixtures.SpawnAction(t, launcher)

	_, err := executor.Run(ctx, json.RawMessage(`{"package":"https://github.com/DeFoS-AI/commander-example-agent"}`))

	if err != nil || len(launcher.Calls()) != 1 {
		t.Fatalf("trusted source did not proceed directly: %v", err)
	}
	select {
	case <-questions.Requests():
		t.Fatal("trusted source requested installation approval")
	default:
	}
}

func TestExternalSourceRequiresQuestionCapability(t *testing.T) {
	ctx := action.WithExecution(t.Context(), fixtures.SourceApprovalExecution())
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: fixtures.SpawnResult()})
	executor := fixtures.SpawnAction(t, launcher)

	_, err := executor.Run(ctx, json.RawMessage(`{"package":"github.com/other/worker-agent@stable"}`))

	if !errors.Is(err, packages.ErrSourceApprovalRequired) || !errors.Is(err, action.ErrQuestionsUnavailable) || len(launcher.Calls()) != 0 {
		t.Fatalf("external install bypassed unavailable question handling: %v", err)
	}
}

func TestSpawnAgentRejectsDeclinedOrInvalidInstallationAnswersBeforeSecretAccess(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	for _, test := range []struct {
		name   string
		change func(*operations.Question)
		err    error
		want   error
	}{
		{name: "declined", change: func(q *operations.Question) { q.Answer = "No" }, want: packages.ErrSourceApprovalDenied},
		{name: "ambiguous", change: func(q *operations.Question) { q.Answer = "Maybe" }, want: packages.ErrSourceApprovalDenied},
		{name: "pending", change: func(q *operations.Question) { q.Status = operations.QuestionPending }, want: action.ErrQuestionRejected},
		{name: "different key", change: func(q *operations.Question) { q.Key = "another-install" }, want: action.ErrQuestionRejected},
		{name: "different source", change: func(q *operations.Question) { q.Text = fixtures.SourceApprovalQuestion }, want: action.ErrQuestionRejected},
		{name: "different options", change: func(q *operations.Question) { q.Options = []string{"Continue", "Cancel"} }, want: action.ErrQuestionRejected},
		{name: "no origin", change: func(q *operations.Question) { q.AnsweredBy = "" }, want: action.ErrQuestionRejected},
		{name: "cancelled", err: context.Canceled, want: context.Canceled},
		{name: "rejected", err: action.ErrQuestionConflict, want: action.ErrQuestionConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			answer := fixtures.SourceApprovalAnswer("Yes", operations.AnswerOperator)
			answer.Text = fixtures.PrivateSourceApprovalQuestion
			if test.change != nil {
				test.change(&answer)
			}
			questions := fakes.NewSourceQuestions()
			questions.Answer(answer, test.err)
			secretRequests := 0
			ctx := action.WithSecrets(action.WithQuestions(action.WithExecution(t.Context(), fixtures.SourceApprovalExecution()), questions), fakes.SecretRequester(func(context.Context, string) ([]byte, error) {
				secretRequests++
				return nil, errors.New("installation question must precede secret access")
			}))
			launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{})
			executor := fixtures.SpawnAction(t, launcher)

			_, err := executor.Run(ctx, json.RawMessage(`{"package":"github.com/other/worker-agent@stable","visibility":"private"}`))

			if !errors.Is(err, test.want) || secretRequests != 0 || len(launcher.Calls()) != 0 {
				t.Fatalf("failed approval reached secret access or launch: err=%v secret_requests=%d launches=%d", err, secretRequests, len(launcher.Calls()))
			}
		})
	}
}

func TestSourceApprovalKeepsPackagePinAndExecutionScope(t *testing.T) {
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	execution, _ := action.ExecutionFromContext(ctx)
	approved := fixtures.SourceApprovalExecution()
	approved.CommandID = execution.CommandID
	questions := fakes.NewSourceQuestions()
	questions.Answer(fixtures.SourceApprovalAnswer("Yes", operations.AnswerModel), nil)
	ctx = action.WithQuestions(action.WithExecution(ctx, approved), questions)
	ctx, err := packages.AuthorizeSource(ctx, fixtures.UntrustedSource, agentpackages.Public)
	if err != nil {
		t.Fatal(err)
	}
	resolver, source := fixtures.SourceApprovalResolver(t)

	pin, err := runtimes.PinPackage(ctx, requestID, "https://github.com/other/worker-agent", resolver)

	if err != nil || pin != "github.com/other/worker-agent@v0.1.0" {
		t.Fatalf("approved source could not select its exact version: %q, %v", pin, err)
	}
	source.SetLatest(2)
	retried, err := runtimes.PinPackage(ctx, requestID, fixtures.UntrustedSource, resolver)
	if err != nil || retried != pin || source.RequestCount() != 1 || len(journal.Checkpoints()) != 1 {
		t.Fatalf("approved retry changed its saved pin: %q, %v, requests=%d", retried, err, source.RequestCount())
	}
	for _, test := range []struct {
		name      string
		context   context.Context
		reference string
	}{
		{"different source", ctx, "github.com/other/different-agent@stable"},
		{"different version", ctx, "github.com/other/worker-agent@v1.2.3"},
		{"new context", t.Context(), fixtures.UntrustedSource},
		{"different execution", action.WithExecution(ctx, action.Execution{CommandID: id.MessageID(), Work: approved.Work}), fixtures.UntrustedSource},
		{"different operation", action.WithExecution(ctx, action.Execution{CommandID: approved.CommandID, Work: fixtures.SourceApprovalExecution().Work}), fixtures.UntrustedSource},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := runtimes.PinPackage(test.context, requestID, test.reference, resolver)

			if !errors.Is(err, packages.ErrSourceApprovalRequired) || source.RequestCount() != 1 || len(journal.Checkpoints()) != 1 {
				t.Fatalf("approval escaped its source/execution scope: %v", err)
			}
		})
	}
}

func receiveSourceQuestion(t *testing.T, ctx context.Context, questions *fakes.SourceQuestions) fakes.SourceQuestionRequest {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case request := <-questions.Requests():
		return request
	case <-ctx.Done():
		t.Fatal("installation question was not requested")
	case <-timer.C:
		t.Fatal("installation question was not requested before its deadline")
	}
	return fakes.SourceQuestionRequest{}
}

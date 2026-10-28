package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestLateExecutionOutcomeRestoresOriginalTaskAndArtifact(t *testing.T) {
	r, execution, original, interrupted := fixtures.InterruptedExecution(t, fixtures.Config())
	observation := fixtures.ObserveLate(t, execution)
	if observation.Agent != original.Agent || !reflect.DeepEqual(observation.Call, original.Call) {
		t.Fatalf("observation changed the dispatched execution: original=%+v observed=%+v", original, observation)
	}
	result := fixtures.LateArtifactOutput(original)
	observation.Reply <- fakes.ObservedOutcome{Output: result}
	settled := fixtures.AwaitOperation(t, r, interrupted.ID, func(op operations.Operation) bool {
		return op.Tasks[0].Status == operations.TaskSucceeded
	})
	got := settled.Tasks[0]
	before := interrupted.Tasks[0]
	if len(settled.Tasks) != 1 || got.ID != before.ID || got.CommandID != before.CommandID || got.AgentID != before.AgentID || got.Error != "" || !reflect.DeepEqual(got.Result, &result) || !reflect.DeepEqual(got.DeadlineAt, before.DeadlineAt) {
		t.Fatalf("late result lost the original task or artifact: %+v", got)
	}
	if got.Continuation == nil || got.Continuation.DecisionBy != operations.DecisionModel || got.Continuation.Status != operations.ContinuationPending {
		t.Fatalf("late success did not return its result for model review: %+v", got.Continuation)
	}
	if execution.Dispatches() != 1 {
		t.Fatalf("late reconciliation dispatched %d executions", execution.Dispatches())
	}
}

func TestInconclusiveLateObservationsPreserveInterruptedEvidence(t *testing.T) {
	r, execution, original, interrupted := fixtures.InterruptedExecution(t, fixtures.Config())
	before := interrupted.Tasks[0]
	interruptionEvents := countTaskInterruptions(interrupted)
	observation := fixtures.ObserveLate(t, execution)
	for _, failure := range []error{executions.ErrIndeterminate, errors.New("execution store unavailable"), context.DeadlineExceeded} {
		observation.Reply <- fakes.ObservedOutcome{Err: failure}
		// A new request proves the preceding observation has been handled.
		observation = fixtures.ObserveLate(t, execution)
		current, err := r.Get(t.Context(), interrupted.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(current.Tasks[0], before) || countTaskInterruptions(current) != interruptionEvents {
			t.Fatalf("inconclusive observation replaced the original interruption: before=%+v after=%+v", before, current.Tasks[0])
		}
	}
	observation.Reply <- fakes.ObservedOutcome{Output: fixtures.LateArtifactOutput(original)}
	fixtures.AwaitOperation(t, r, interrupted.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if execution.MaximumObservers() != 1 || execution.Dispatches() != 1 {
		t.Fatalf("observations overlapped or dispatched work: max=%d dispatches=%d", execution.MaximumObservers(), execution.Dispatches())
	}
}

func TestLateExecutionFailureBecomesKnownOutcome(t *testing.T) {
	r, execution, original, interrupted := fixtures.InterruptedExecution(t, fixtures.Config())
	failure := &executions.CommandFailure{AgentID: original.Agent, CommandID: original.Call.Work.TaskID,
		Detail: executions.CommandError{ErrorType: "build_failed", Message: "generated agent tests failed"}}
	fixtures.ObserveLate(t, execution).Reply <- fakes.ObservedOutcome{Err: fmt.Errorf("saved result: %w", failure)}
	failed := fixtures.AwaitOperation(t, r, interrupted.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskFailed })
	if failed.Tasks[0].Result != nil || !strings.Contains(failed.Tasks[0].Error, failure.Detail.Message) || execution.Dispatches() != 1 {
		t.Fatalf("late command failure was lost or execution repeated: %+v", failed.Tasks[0])
	}
}

func TestLateExecutionOutcomeSettlesTerminalOperation(t *testing.T) {
	for _, status := range []operations.Status{operations.Completed, operations.Cancelled, operations.Failed} {
		t.Run(string(status), func(t *testing.T) {
			config := fixtures.Config()
			catalog := &fakes.LateOutcomeCatalog{AgentCatalog: config.AgentCatalog}
			config.AgentCatalog = catalog
			config.ReportStore = fakes.NewTerminalReportStore()
			r, execution, original, interrupted := fixtures.InterruptedExecution(t, config)
			observation := fixtures.ObserveLate(t, execution)
			var terminal operations.Operation
			var err error
			switch status {
			case operations.Completed:
				terminal, err = r.Complete(t.Context(), commands.CompleteOperationCommand{OperationID: interrupted.ID,
					Output: json.RawMessage(`{"limitations":["The execution outcome is still unknown"]}`)})
			case operations.Cancelled:
				terminal, err = r.Cancel(t.Context(), commands.CancelOperationCommand{OperationID: interrupted.ID})
			case operations.Failed:
				terminal, err = r.Fail(t.Context(), commands.FailOperationCommand{OperationID: interrupted.ID, Reason: "Stop the review"})
			}
			if err != nil {
				t.Fatal(err)
			}
			var savedReport reports.Report
			if status == operations.Completed {
				savedReport, err = r.GetReport(t.Context(), terminal.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if terminal.AssessProgress(time.Now()).Settled {
				t.Fatal("unknown execution was marked settled during finalization")
			}
			// Terminal evidence must remain recoverable when proposal lookup fails.
			catalog.SetUnavailable(true)
			result := fixtures.LateArtifactOutput(original)
			result.NextActions = []actions.NextAction{{ActionName: "follow-up", Input: json.RawMessage(`{}`)}}
			observation.Reply <- fakes.ObservedOutcome{Output: result}
			settled := fixtures.AwaitOperation(t, r, terminal.ID, func(op operations.Operation) bool { return op.AssessProgress(time.Now()).Settled })
			if settled.Status != status || len(settled.Tasks) != 1 || !reflect.DeepEqual(settled.Tasks[0].Result, &result) || settled.ReportID != terminal.ReportID || !reflect.DeepEqual(settled.Output, terminal.Output) || settled.Error != terminal.Error {
				t.Fatalf("late result changed finalization or lost its artifact: %+v", settled)
			}
			if status == operations.Completed {
				report, err := r.GetReport(t.Context(), terminal.ID)
				if err != nil || !reflect.DeepEqual(report, savedReport) {
					t.Fatalf("late evidence replaced the immutable report: %+v, %v", report, err)
				}
			}
			fixtures.CloseRunner(t, r)
			if execution.Dispatches() != 1 {
				t.Fatalf("terminal reconciliation dispatched follow-up work: %d executions", execution.Dispatches())
			}
		})
	}
}

func TestLateExecutionOutcomePreservesAnsweredWaitQuestion(t *testing.T) {
	r, execution, original, interrupted := fixtures.InterruptedExecution(t, fixtures.Config())
	observation := fixtures.ObserveLate(t, execution)
	question, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: interrupted.ID,
		TaskID: interrupted.Tasks[0].ID, Version: interrupted.Tasks[0].Continuation.Version,
		Text: "Wait for the current execution?", Options: []string{"Wait", "Stop"}})
	if err != nil {
		t.Fatal(err)
	}
	answered, err := r.Answer(t.Context(), commands.AnswerQuestionCommand{OperationID: interrupted.ID, QuestionID: question.ID, Answer: "Wait"})
	if err != nil {
		t.Fatal(err)
	}
	observation.Reply <- fakes.ObservedOutcome{Output: fixtures.LateArtifactOutput(original)}
	settled := fixtures.AwaitOperation(t, r, interrupted.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	continuation := settled.Tasks[0].Continuation
	if !reflect.DeepEqual(settled.Questions, answered.Questions) || continuation == nil || continuation.QuestionID != question.ID || continuation.DecisionBy != operations.DecisionModel || continuation.Status != operations.ContinuationPending {
		t.Fatalf("late outcome lost operator guidance or its model decision: %+v", settled)
	}
	if execution.Dispatches() != 1 {
		t.Fatalf("answering Wait repeated execution: %d dispatches", execution.Dispatches())
	}
}

func TestLateExecutionObservationContinuesAfterRestart(t *testing.T) {
	config := fixtures.Config()
	first, execution, original, interrupted := fixtures.InterruptedExecution(t, config)
	fixtures.ObserveLate(t, execution)
	fixtures.CloseRunner(t, first)
	restarted := fixtures.NewRunner(t, config, execution.Executor())
	fixtures.ObserveLate(t, execution).Reply <- fakes.ObservedOutcome{Err: executions.ErrIndeterminate}
	observation := fixtures.ObserveLate(t, execution)
	if observation.Agent != original.Agent || !reflect.DeepEqual(observation.Call, original.Call) {
		t.Fatalf("restart changed execution identity: %+v", observation)
	}
	observation.Reply <- fakes.ObservedOutcome{Output: fixtures.LateArtifactOutput(original)}
	fixtures.AwaitOperation(t, restarted, interrupted.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if execution.Dispatches() != 1 {
		t.Fatalf("restarted reconciliation repeated execution: %d dispatches", execution.Dispatches())
	}
}

func TestCloseJoinsLateExecutionObservers(t *testing.T) {
	r, execution, _, _ := fixtures.InterruptedExecution(t, fixtures.Config())
	fixtures.ObserveLate(t, execution)
	fixtures.CloseRunner(t, r)
	if active := execution.ActiveObservers(); active != 0 {
		t.Fatalf("Close left %d reconciliation observers running", active)
	}
}

func TestLateExecutionOutcomeWaitsForUncertainWriteRecovery(t *testing.T) {
	config := fixtures.Config()
	config.TaskTimeout = 30 * time.Millisecond
	failure := errors.New("directory sync failed")
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore,
		AfterResolve: func(operations.Operation) error {
			return errors.Join(operations.ErrWriteUncertain, failure)
		}}
	execution := fakes.NewLateExecution()
	first, err := runner.NewRunner(t.Context(), execution.Executor(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := fixtures.Start(t, first)
	fixtures.Assign(t, first, op.ID, "inspect")
	original := fixtures.Receive(t, execution.Started())
	interrupted := fixtures.AwaitOperation(t, first, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskInterrupted })
	observation := fixtures.ObserveLate(t, execution)
	_, resolveErr := first.Resolve(t.Context(), commands.ResolveContinuationCommand{OperationID: op.ID,
		TaskID: interrupted.Tasks[0].ID, Version: interrupted.Tasks[0].Continuation.Version, Actions: []actions.NextAction{}})
	_, waitErr := first.WaitTask(t.Context(), op.ID, interrupted.Tasks[0].ID)
	_, eventErr := first.Events(t.Context(), op.ID, 0, false)
	for _, err := range []error{resolveErr, waitErr, eventErr} {
		if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
			t.Fatalf("uncertain continuation write was hidden: %v", err)
		}
	}
	before, err := first.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := fixtures.LateArtifactOutput(original)
	observation.Reply <- fakes.ObservedOutcome{Output: result}
	select {
	case <-observation.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("late observer did not return its known outcome")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := first.Close(ctx); !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
		t.Fatalf("shutdown lost the uncertain write error: %v", err)
	}
	stored, err := first.Get(t.Context(), op.ID)
	if err != nil || !reflect.DeepEqual(stored, before) {
		t.Fatalf("in-flight outcome mutated an operation suspended by an uncertain write: %+v, %v", stored, err)
	}
	restarted := fixtures.NewRunner(t, config, execution.Executor())
	fixtures.ObserveLate(t, execution).Reply <- fakes.ObservedOutcome{Output: result}
	recovered := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if !reflect.DeepEqual(recovered.Tasks[0].Result, &result) || execution.Dispatches() != 1 {
		t.Fatalf("recovery lost the saved result or repeated execution: %+v, dispatches=%d", recovered.Tasks[0], execution.Dispatches())
	}
}

func countTaskInterruptions(op operations.Operation) int {
	count := 0
	for _, event := range op.Events {
		if event.Type == operations.TaskInterruptedEvent {
			count++
		}
	}
	return count
}

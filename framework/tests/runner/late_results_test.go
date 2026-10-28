package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
)

func TestLateExecutionOutcomeRestoresOriginalTaskAndArtifact(t *testing.T) {
	r, execution, original, interrupted := interruptedExecution(t, fixture())
	observation := execution.observe(t)
	if observation.agent != original.agent || !reflect.DeepEqual(observation.call, original.call) {
		t.Fatalf("observation changed the dispatched execution: original=%+v observed=%+v", original, observation)
	}
	result := lateArtifactOutput(original)
	observation.reply <- observedOutcome{output: result}
	settled := awaitOperation(t, r, interrupted.ID, func(op operations.Operation) bool {
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
	if execution.dispatches.Load() != 1 {
		t.Fatalf("late reconciliation dispatched %d executions", execution.dispatches.Load())
	}
}

func TestInconclusiveLateObservationsPreserveInterruptedEvidence(t *testing.T) {
	r, execution, original, interrupted := interruptedExecution(t, fixture())
	before := interrupted.Tasks[0]
	interruptionEvents := countTaskInterruptions(interrupted)
	observation := execution.observe(t)
	for _, failure := range []error{executions.ErrIndeterminate, errors.New("execution store unavailable"), context.DeadlineExceeded} {
		observation.reply <- observedOutcome{err: failure}
		// A new request proves the preceding observation has been handled.
		observation = execution.observe(t)
		current, err := r.Get(t.Context(), interrupted.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(current.Tasks[0], before) || countTaskInterruptions(current) != interruptionEvents {
			t.Fatalf("inconclusive observation replaced the original interruption: before=%+v after=%+v", before, current.Tasks[0])
		}
	}
	observation.reply <- observedOutcome{output: lateArtifactOutput(original)}
	awaitOperation(t, r, interrupted.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if execution.maximumObservers.Load() != 1 || execution.dispatches.Load() != 1 {
		t.Fatalf("observations overlapped or dispatched work: max=%d dispatches=%d", execution.maximumObservers.Load(), execution.dispatches.Load())
	}
}

func TestLateExecutionFailureBecomesKnownOutcome(t *testing.T) {
	r, execution, original, interrupted := interruptedExecution(t, fixture())
	failure := &executions.CommandFailure{AgentID: original.agent, CommandID: original.call.Work.TaskID,
		Detail: executions.CommandError{ErrorType: "build_failed", Message: "generated agent tests failed"}}
	execution.observe(t).reply <- observedOutcome{err: fmt.Errorf("saved result: %w", failure)}
	failed := awaitOperation(t, r, interrupted.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskFailed })
	if failed.Tasks[0].Result != nil || !strings.Contains(failed.Tasks[0].Error, failure.Detail.Message) || execution.dispatches.Load() != 1 {
		t.Fatalf("late command failure was lost or execution repeated: %+v", failed.Tasks[0])
	}
}

func TestLateExecutionOutcomeSettlesTerminalOperation(t *testing.T) {
	for _, status := range []operations.Status{operations.Completed, operations.Cancelled, operations.Failed} {
		t.Run(string(status), func(t *testing.T) {
			config := fixture()
			catalog := &lateOutcomeCatalog{AgentCatalog: config.AgentCatalog}
			config.AgentCatalog = catalog
			config.ReportStore = &terminalReportStore{records: make(map[string]reports.Report)}
			r, execution, original, interrupted := interruptedExecution(t, config)
			observation := execution.observe(t)
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
			catalog.unavailable.Store(true)
			result := lateArtifactOutput(original)
			result.NextActions = []actions.NextAction{{ActionName: "follow-up", Input: json.RawMessage(`{}`)}}
			observation.reply <- observedOutcome{output: result}
			settled := awaitOperation(t, r, terminal.ID, func(op operations.Operation) bool { return op.AssessProgress(time.Now()).Settled })
			if settled.Status != status || len(settled.Tasks) != 1 || !reflect.DeepEqual(settled.Tasks[0].Result, &result) || settled.ReportID != terminal.ReportID || !reflect.DeepEqual(settled.Output, terminal.Output) || settled.Error != terminal.Error {
				t.Fatalf("late result changed finalization or lost its artifact: %+v", settled)
			}
			if status == operations.Completed {
				report, err := r.GetReport(t.Context(), terminal.ID)
				if err != nil || !reflect.DeepEqual(report, savedReport) {
					t.Fatalf("late evidence replaced the immutable report: %+v, %v", report, err)
				}
			}
			closeRunner(t, r)
			if execution.dispatches.Load() != 1 {
				t.Fatalf("terminal reconciliation dispatched follow-up work: %d executions", execution.dispatches.Load())
			}
		})
	}
}

func TestLateExecutionOutcomePreservesAnsweredWaitQuestion(t *testing.T) {
	r, execution, original, interrupted := interruptedExecution(t, fixture())
	observation := execution.observe(t)
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
	observation.reply <- observedOutcome{output: lateArtifactOutput(original)}
	settled := awaitOperation(t, r, interrupted.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	continuation := settled.Tasks[0].Continuation
	if !reflect.DeepEqual(settled.Questions, answered.Questions) || continuation == nil || continuation.QuestionID != question.ID || continuation.DecisionBy != operations.DecisionModel || continuation.Status != operations.ContinuationPending {
		t.Fatalf("late outcome lost operator guidance or its model decision: %+v", settled)
	}
	if execution.dispatches.Load() != 1 {
		t.Fatalf("answering Wait repeated execution: %d dispatches", execution.dispatches.Load())
	}
}

func TestLateExecutionObservationContinuesAfterRestart(t *testing.T) {
	config := fixture()
	first, execution, original, interrupted := interruptedExecution(t, config)
	execution.observe(t)
	closeRunner(t, first)
	restarted := newRunner(t, config, execution.executor())
	execution.observe(t).reply <- observedOutcome{err: executions.ErrIndeterminate}
	observation := execution.observe(t)
	if observation.agent != original.agent || !reflect.DeepEqual(observation.call, original.call) {
		t.Fatalf("restart changed execution identity: %+v", observation)
	}
	observation.reply <- observedOutcome{output: lateArtifactOutput(original)}
	awaitOperation(t, restarted, interrupted.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if execution.dispatches.Load() != 1 {
		t.Fatalf("restarted reconciliation repeated execution: %d dispatches", execution.dispatches.Load())
	}
}

func TestCloseJoinsLateExecutionObservers(t *testing.T) {
	r, execution, _, _ := interruptedExecution(t, fixture())
	execution.observe(t)
	closeRunner(t, r)
	if active := execution.activeObservers.Load(); active != 0 {
		t.Fatalf("Close left %d reconciliation observers running", active)
	}
}

func TestLateExecutionOutcomeWaitsForUncertainWriteRecovery(t *testing.T) {
	config := fixture()
	config.TaskTimeout = 30 * time.Millisecond
	failure := errors.New("directory sync failed")
	config.OperationStore = &uncertainWriteStore{OperationStore: config.OperationStore,
		afterResolve: func(operations.Operation) error {
			return errors.Join(operations.ErrWriteUncertain, failure)
		}}
	execution := &lateExecution{started: make(chan invocation, 1), observations: make(chan lateObservation, 16)}
	first, err := runner.NewRunner(t.Context(), execution.executor(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := start(t, first)
	assign(t, first, op.ID, "inspect")
	original := receive(t, execution.started)
	interrupted := awaitOperation(t, first, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskInterrupted })
	observation := execution.observe(t)
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
	result := lateArtifactOutput(original)
	observation.reply <- observedOutcome{output: result}
	select {
	case <-observation.done:
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
	restarted := newRunner(t, config, execution.executor())
	execution.observe(t).reply <- observedOutcome{output: result}
	recovered := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	if !reflect.DeepEqual(recovered.Tasks[0].Result, &result) || execution.dispatches.Load() != 1 {
		t.Fatalf("recovery lost the saved result or repeated execution: %+v, dispatches=%d", recovered.Tasks[0], execution.dispatches.Load())
	}
}

type observedOutcome struct {
	output actions.ActionOutput
	err    error
}

type lateObservation struct {
	agent string
	call  actions.ActionCall
	reply chan observedOutcome
	done  chan struct{}
}

type lateExecution struct {
	dispatches       atomic.Int32
	activeObservers  atomic.Int32
	maximumObservers atomic.Int32
	started          chan invocation
	observations     chan lateObservation
}

func interruptedExecution(t *testing.T, config runner.Config) (*runner.Runner, *lateExecution, invocation, operations.Operation) {
	t.Helper()
	config.TaskTimeout = 30 * time.Millisecond
	execution := &lateExecution{started: make(chan invocation, 1), observations: make(chan lateObservation, 16)}
	r := newRunner(t, config, execution.executor())
	op := start(t, r)
	assign(t, r, op.ID, "inspect")
	original := receive(t, execution.started)
	interrupted := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskInterrupted })
	return r, execution, original, interrupted
}

func (e *lateExecution) executor() executor {
	return executor{run: func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		e.dispatches.Add(1)
		select {
		case e.started <- invocation{agent: agent, call: call}:
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
		<-ctx.Done()
		return actions.ActionOutput{}, ctx.Err()
	}, recover: func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		active := e.activeObservers.Add(1)
		defer e.activeObservers.Add(-1)
		for maximum := e.maximumObservers.Load(); active > maximum; maximum = e.maximumObservers.Load() {
			if e.maximumObservers.CompareAndSwap(maximum, active) {
				break
			}
		}
		observation := lateObservation{agent: agent, call: call, reply: make(chan observedOutcome, 1), done: make(chan struct{})}
		defer close(observation.done)
		select {
		case e.observations <- observation:
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
		select {
		case result := <-observation.reply:
			return result.output, result.err
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
	}}
}

type lateOutcomeCatalog struct {
	runner.AgentCatalog
	unavailable atomic.Bool
}

func (c *lateOutcomeCatalog) Agents(ctx context.Context) ([]agents.Agent, error) {
	if c.unavailable.Load() {
		return nil, errors.New("agent catalog unavailable")
	}
	return c.AgentCatalog.Agents(ctx)
}

func (e *lateExecution) observe(t *testing.T) lateObservation {
	t.Helper()
	select {
	case observation := <-e.observations:
		return observation
	case <-time.After(5 * time.Second):
		t.Fatal("expected observation of an interrupted execution")
		return lateObservation{}
	}
}

func lateArtifactOutput(original invocation) actions.ActionOutput {
	result := output(original.call.ActionName)
	result.Artifacts = []artifacts.Artifact{{ID: id.New(), Name: "Agent source", AgentID: original.agent,
		ActionName: original.call.ActionName, CommandID: original.call.Work.TaskID, CreatedAt: time.Now().UTC(),
		Files: []artifacts.ArtifactFile{{ID: id.New(), Name: "source.tar.gz", MediaType: "application/gzip", Size: 7, SHA256: strings.Repeat("a", 64)}}}}
	return result
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

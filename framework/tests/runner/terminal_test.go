package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestCompletionWriteFailurePreservesReportOrderingAndRetry(t *testing.T) {
	for _, failingStore := range []string{"report", "operation"} {
		t.Run(failingStore, func(t *testing.T) {
			config := fixtures.Config()
			reportStore := fakes.NewTerminalReportStore()
			config.ReportStore = reportStore
			exec, calls := fakes.ControlledExecutor()
			r := fixtures.NewRunner(t, config, exec)
			op := fixtures.Start(t, r)
			fixtures.Assign(t, r, op.ID, "inspect")
			fixtures.Receive(t, calls).Reply <- fixtures.Output("inspect")
			before := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
			failure := errors.New("storage unavailable")
			if failingStore == "report" {
				reportStore.FailNext(failure)
			} else {
				config.OperationStore.(*fakes.OperationStore).FailNextUpdate(failure)
			}
			command := commands.CompleteOperationCommand{OperationID: op.ID, Output: json.RawMessage(`{"review":"done"}`), CompletedBy: operations.CompletionModel}

			_, completeErr := r.Complete(t.Context(), command)
			stored, err := r.Get(t.Context(), op.ID)
			if !errors.Is(completeErr, failure) || err != nil || !reflect.DeepEqual(stored, before) {
				t.Fatalf("failed completion changed durable state: operation=%+v, complete=%v, get=%v", stored, completeErr, err)
			}
			events, err := r.Events(t.Context(), op.ID, 0, false)
			if err != nil || !reflect.DeepEqual(events, before.Events) {
				t.Fatalf("failed completion changed runner events: %+v, %v", events, err)
			}
			if _, err := r.GetReport(t.Context(), op.ID); !errors.Is(err, reports.ErrUnknownReport) {
				t.Fatalf("failed completion published a report reference: %v", err)
			}
			wantReports := 0
			if failingStore == "operation" {
				wantReports = 1
			}
			if reportStore.Count() != wantReports {
				t.Fatalf("report was not published before the operation update: reports=%d", reportStore.Count())
			}

			completed, err := r.Complete(t.Context(), command)
			if err != nil || completed.Status != operations.Completed || completed.ReportID == "" || !reflect.DeepEqual(completed.Output, command.Output) {
				t.Fatalf("completion retry failed: %+v, %v", completed, err)
			}
			continuation := completed.Tasks[0].Continuation
			if continuation.Status != operations.ContinuationResolved || continuation.Version != before.Tasks[0].Continuation.Version+1 || len(completed.Events) != len(before.Events)+1 || completed.Events[len(completed.Events)-1].Type != operations.OperationCompleted {
				t.Fatalf("retry did not atomically close the model decision and record completion: %+v", completed)
			}
			report, err := r.GetReport(t.Context(), op.ID)
			if err != nil || report.ID != completed.ReportID || report.OperationID != op.ID || !reflect.DeepEqual(report.Output, command.Output) || reportStore.Count() != wantReports+1 {
				t.Fatalf("completion does not reference the published report: %+v, %v", report, err)
			}
			if _, err := r.Complete(t.Context(), command); !errors.Is(err, operations.ErrFinished) || reportStore.Count() != wantReports+1 {
				t.Fatalf("repeated completion published another report: %v", err)
			}
		})
	}
}

func TestCompletionRechecksStateAfterPublishingReport(t *testing.T) {
	config := fixtures.Config()
	reportStore := fakes.NewTerminalReportStore()
	config.ReportStore = reportStore
	r := fixtures.NewRunner(t, config, fakes.Executor{})
	op := fixtures.Start(t, r)
	var failed operations.Operation
	reportStore.AfterPut(func(ctx context.Context, report reports.Report) error {
		var err error
		failed, err = config.OperationStore.Fail(ctx, report.OperationID, "concurrent failure")
		return err
	})

	_, completeErr := r.Complete(t.Context(), commands.CompleteOperationCommand{OperationID: op.ID, Output: json.RawMessage(`{}`), CompletedBy: operations.CompletionModel})
	stored, err := r.Get(t.Context(), op.ID)
	if !errors.Is(completeErr, operations.ErrFinished) || err != nil || failed.Status != operations.Failed || !reflect.DeepEqual(stored, failed) || reportStore.Count() != 1 {
		t.Fatalf("completion overwrote a terminal state committed during report publication: %+v, complete=%v, get=%v", stored, completeErr, err)
	}
}

func TestUncertainCompletionRetainsReportThroughRecovery(t *testing.T) {
	config := fixtures.Config()
	reportStore := fakes.NewTerminalReportStore()
	config.ReportStore = reportStore
	failure := errors.New("directory sync failed")
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore, AfterComplete: func(operations.Operation) error {
		return errors.Join(operations.ErrWriteUncertain, failure)
	}}
	first, err := runner.NewRunner(t.Context(), fakes.Executor{}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := fixtures.Start(t, first)
	command := commands.CompleteOperationCommand{OperationID: op.ID, Output: json.RawMessage(`{"review":"done"}`), CompletedBy: operations.CompletionOperator}
	_, completeErr := first.Complete(t.Context(), command)
	_, eventErr := first.Events(t.Context(), op.ID, 0, false)
	_, retryErr := first.Complete(t.Context(), command)
	closeErr := first.Close(t.Context())
	for _, err := range []error{completeErr, eventErr, retryErr, closeErr} {
		if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
			t.Fatalf("uncertain completion was hidden: %v", err)
		}
	}
	stored, err := first.Get(t.Context(), op.ID)
	if err != nil || stored.Status != operations.Completed || stored.CompletedBy != command.CompletedBy || stored.ReportID == "" || !reflect.DeepEqual(stored.Output, command.Output) || len(stored.Events) != len(op.Events)+1 {
		t.Fatalf("uncertain completion lost its visible snapshot: %+v, %v", stored, err)
	}
	savedReport, err := first.GetReport(t.Context(), op.ID)
	if err != nil || savedReport.ID != stored.ReportID || savedReport.OperationID != op.ID || !reflect.DeepEqual(savedReport.Output, stored.Output) {
		t.Fatalf("uncertain completion lost its published report: %+v, %v", savedReport, err)
	}

	restarted := fixtures.NewRunner(t, config, fakes.Executor{})
	recovered, err := restarted.Get(t.Context(), op.ID)
	if err != nil || !reflect.DeepEqual(recovered, stored) {
		t.Fatalf("recovery changed the completed operation: %+v, %v", recovered, err)
	}
	recoveredReport, err := restarted.GetReport(t.Context(), op.ID)
	if err != nil || !reflect.DeepEqual(recoveredReport, savedReport) || reportStore.Count() != 1 {
		t.Fatalf("recovery lost or replaced the completed report: %+v, %v", recoveredReport, err)
	}
	events, err := restarted.Events(t.Context(), op.ID, 0, false)
	if err != nil || !reflect.DeepEqual(events, stored.Events) {
		t.Fatalf("recovery changed completion events: %+v, %v", events, err)
	}
}

func TestUncertainFailureRecoversRunningResultsWithoutNewWork(t *testing.T) {
	config := fixtures.Config()
	config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
		m.Rules = missions.RuleSet{
			missions.AggregateResults: {ForActions: []string{"discover"}},
		}
	})
	failure := errors.New("directory sync failed")
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore, AfterFail: func(operations.Operation) error {
		return errors.Join(operations.ErrWriteUncertain, failure)
	}}
	exec, calls := fakes.ControlledExecutor()
	first, err := runner.NewRunner(t.Context(), fakes.DurableExecutor{Executor: exec}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = first.Close(ctx)
	})
	op := fixtures.Start(t, first)
	running := fixtures.Assign(t, first, op.ID, "discover")
	original := fixtures.Receive(t, calls)
	queued := fixtures.Assign(t, first, op.ID, "inspect")
	command := commands.FailOperationCommand{OperationID: op.ID, Reason: "review cannot continue"}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, failErr := first.Fail(ctx, command)
	_, waitErr := first.WaitTask(ctx, op.ID, running.ID)
	_, eventErr := first.Events(ctx, op.ID, 0, false)
	_, retryErr := first.Fail(ctx, command)
	_, assignErr := first.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, ActionName: "inspect"})
	closeErr := first.Close(ctx)
	for _, err := range []error{failErr, waitErr, eventErr, retryErr, assignErr, closeErr} {
		if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, failure) {
			t.Fatalf("uncertain failure was hidden: %v", err)
		}
	}
	stored, err := first.Get(ctx, op.ID)
	if err != nil || stored.Status != operations.Failed || stored.Error != command.Reason || len(stored.Tasks) != 2 || stored.Tasks[0].Status != operations.TaskRunning || stored.Tasks[1].ID != queued.ID || stored.Tasks[1].Status != operations.TaskCancelled {
		t.Fatalf("uncertain failure lost its visible snapshot: %+v, %v", stored, err)
	}

	savedResult := fixtures.Output("discover", actions.NextAction{ActionName: "inspect"})
	observed := make(chan fakes.Invocation, 2)
	exec.Recover = func(_ context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
		observed <- fakes.Invocation{Agent: agent, Call: call}
		return savedResult, nil
	}
	restarted, err := runner.NewRunner(t.Context(), fakes.DurableExecutor{Executor: exec}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixtures.CloseRunner(t, restarted) })
	recovered := fixtures.Receive(t, observed)
	if recovered.Agent != original.Agent || !reflect.DeepEqual(recovered.Call, original.Call) {
		t.Fatalf("failure recovery changed the dispatched command: %+v", recovered)
	}
	settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	fixtures.CloseRunner(t, restarted)
	if settled.Status != operations.Failed || settled.Error != command.Reason || len(settled.Tasks) != 2 || settled.Tasks[0].ID != running.ID || !reflect.DeepEqual(settled.Tasks[0].Result, &savedResult) || !reflect.DeepEqual(settled.Tasks[1], stored.Tasks[1]) {
		t.Fatalf("recovery lost failure or created follow-up work: %+v", settled)
	}
	failures := 0
	for _, event := range settled.Events {
		if event.Type == operations.OperationFailed {
			failures++
			if event.Message != command.Reason {
				t.Fatalf("failure event lost its reason: %+v", event)
			}
		}
	}
	if failures != 1 {
		t.Fatalf("recovery recorded failure %d times", failures)
	}
	select {
	case call := <-calls:
		t.Fatalf("failure dispatched queued or automatic work: %+v", call)
	case call := <-observed:
		t.Fatalf("recovery observed more than the running task: %+v", call)
	default:
	}
}

package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestCompletionDraftWaitsForOperatorAndSurvivesRecovery(t *testing.T) {
	config := fixtures.Config()
	catalog := config.MissionStore.(*fakes.MissionStore)
	catalog.Change(func(m *missions.Mission) {
		m.Completion = &missions.CompletionPolicy{DecisionBy: missions.Operator}
		m.OutputSchema = json.RawMessage(`{"type":"object","required":["summary"]}`)
	})
	reports := fakes.NewTerminalReportStore()
	config.ReportStore = reports
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewRunner(t, config, exec)
	op := fixtures.Start(t, r)
	fixtures.Assign(t, r, op.ID, "inspect")
	fixtures.Receive(t, calls).Reply <- fixtures.Output("inspect")
	before := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	output := json.RawMessage(`{"summary":"Ready for review","evidence_id":9007199254740993,"path":"\u0061\/b"}`)
	command := commands.CompleteOperationCommand{OperationID: op.ID, Output: output, CompletedBy: operations.CompletionModel}

	drafted, err := r.Complete(t.Context(), command)

	if err != nil || !bytes.Equal(drafted.CompletionDraft, output) {
		t.Fatalf("model completion draft was not retained for review: %+v, %v", drafted, err)
	}
	if drafted.Status != before.Status || !reflect.DeepEqual(drafted.Tasks, before.Tasks) || !reflect.DeepEqual(drafted.Questions, before.Questions) ||
		len(drafted.Output) != 0 || drafted.ReportID != "" || drafted.CompletedBy != "" || reports.Count() != 0 {
		t.Fatal("saving a completion draft completed work or published a report")
	}
	draft := bytes.Clone(drafted.CompletionDraft)
	drafted.CompletionDraft[0] = '['
	retained, err := r.Get(t.Context(), op.ID)
	if err != nil || !bytes.Equal(retained.CompletionDraft, draft) {
		t.Fatal("a reader changed the saved completion draft")
	}

	replayed, err := r.Complete(t.Context(), command)

	if err != nil || !reflect.DeepEqual(replayed, retained) || reports.Count() != 0 {
		t.Fatal("repeating model completion changed or approved its draft")
	}
	fixtures.CloseRunner(t, r)
	catalog.Change(func(m *missions.Mission) { m.Completion.DecisionBy = missions.Model })

	restarted := fixtures.NewRunner(t, config, fakes.Executor{})
	restored, err := restarted.Get(t.Context(), op.ID)

	if err != nil || !bytes.Equal(restored.CompletionDraft, draft) || restored.Status.Terminal() ||
		restored.Mission.Completion.DecisionBy != missions.Operator || reports.Count() != 0 {
		t.Fatalf("recovery lost the draft or changed completion authority: %+v, %v", restored, err)
	}
	approved := json.RawMessage(`{"summary":"Reviewed and edited by the operator"}`)

	completed, err := restarted.Complete(t.Context(), commands.CompleteOperationCommand{
		OperationID: op.ID, Output: approved, CompletedBy: operations.CompletionOperator,
	})
	report, reportErr := restarted.GetReport(t.Context(), op.ID)

	if err != nil || reportErr != nil || completed.Status != operations.Completed || completed.CompletedBy != operations.CompletionOperator ||
		!bytes.Equal(report.Output, approved) || len(completed.CompletionDraft) != 0 || reports.Count() != 1 {
		t.Fatalf("operator completion did not save its chosen output and clear the draft: %v, %v", err, reportErr)
	}
}

func TestCompletionDraftRejectsIneligibleOutputWithoutWrites(t *testing.T) {
	for _, scenario := range []string{"active task", "pending question", "invalid output", "missing origin", "unknown origin"} {
		t.Run(scenario, func(t *testing.T) {
			config := fixtures.Config()
			config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
				m.Completion = &missions.CompletionPolicy{DecisionBy: missions.Operator}
				m.OutputSchema = json.RawMessage(`{"type":"object","required":["summary"]}`)
			})
			reports := fakes.NewTerminalReportStore()
			config.ReportStore = reports
			exec, calls := fakes.ControlledExecutor()
			r := fixtures.NewRunner(t, config, exec)
			op := fixtures.Start(t, r)
			command := commands.CompleteOperationCommand{OperationID: op.ID, Output: json.RawMessage(`{"summary":"Reviewed"}`), CompletedBy: operations.CompletionModel}
			var target error
			switch scenario {
			case "active task":
				fixtures.Assign(t, r, op.ID, "inspect")
				fixtures.Receive(t, calls)
				target = operations.ErrActiveTasks
			case "pending question":
				if _, err := r.Ask(t.Context(), commands.AskQuestionCommand{OperationID: op.ID, Text: "Continue?"}); err != nil {
					t.Fatal(err)
				}
				target = operations.ErrQuestionPending
			case "invalid output":
				command.Output = json.RawMessage(`{}`)
				target = domain.ErrInvalidOutput
			case "missing origin":
				command.CompletedBy = ""
			case "unknown origin":
				command.CompletedBy = "service"
			}
			before, err := r.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}

			_, completionErr := r.Complete(t.Context(), command)
			retained, err := r.Get(t.Context(), op.ID)

			if completionErr == nil || target != nil && !errors.Is(completionErr, target) || err != nil ||
				!reflect.DeepEqual(retained, before) || reports.Count() != 0 {
				t.Fatalf("invalid completion draft changed operation state: %v, %v", completionErr, err)
			}
		})
	}
}

func TestUncertainCompletionDraftRequiresRecoveryWithoutReplacement(t *testing.T) {
	config := fixtures.Config()
	config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
		m.Completion = &missions.CompletionPolicy{DecisionBy: missions.Operator}
	})
	reports := fakes.NewTerminalReportStore()
	config.ReportStore = reports
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore,
		AfterSaveCompletionDraft: func(operations.Operation) error { return operations.ErrWriteUncertain }}
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
	command := commands.CompleteOperationCommand{OperationID: op.ID, Output: json.RawMessage(`{"summary":"Review me"}`), CompletedBy: operations.CompletionModel}

	_, completionErr := first.Complete(t.Context(), command)
	_, retryErr := first.Complete(t.Context(), command)
	closeErr := first.Close(t.Context())
	stored, err := first.Get(t.Context(), op.ID)

	if !errors.Is(completionErr, operations.ErrWriteUncertain) || !errors.Is(retryErr, operations.ErrWriteUncertain) ||
		!errors.Is(closeErr, operations.ErrWriteUncertain) || err != nil || len(stored.CompletionDraft) == 0 || reports.Count() != 0 {
		t.Fatal("uncertain completion draft was retried or lost its saved snapshot")
	}

	restarted := fixtures.NewRunner(t, config, fakes.Executor{})
	restored, err := restarted.Get(t.Context(), op.ID)

	if err != nil || !bytes.Equal(restored.CompletionDraft, stored.CompletionDraft) || restored.Status.Terminal() || reports.Count() != 0 {
		t.Fatalf("recovery replaced or approved an uncertain draft: %+v, %v", restored, err)
	}
}

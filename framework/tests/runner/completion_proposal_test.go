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

func TestCompletionProposalWaitsForOperatorAndSurvivesRecovery(t *testing.T) {
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

	proposed, err := r.ProposeCompletion(t.Context(), commands.ProposeCompletionCommand{
		OperationID: op.ID, Output: output, ProposedBy: operations.CompletionModel,
	})

	if err != nil || proposed.CompletionProposal == nil || proposed.CompletionProposal.ProposedBy != operations.CompletionModel ||
		!bytes.Equal(proposed.CompletionProposal.Output, output) {
		t.Fatalf("model proposal was not retained for review: %+v, %v", proposed, err)
	}
	if proposed.Status != before.Status || !reflect.DeepEqual(proposed.Tasks, before.Tasks) || !reflect.DeepEqual(proposed.Questions, before.Questions) ||
		len(proposed.Output) != 0 || proposed.ReportID != "" || proposed.CompletedBy != "" || reports.Count() != 0 {
		t.Fatal("saving proposed output completed work or published a report")
	}
	proposal := proposed.CompletionProposal.Clone()
	proposed.CompletionProposal.Output[0] = '['
	retained, err := r.Get(t.Context(), op.ID)
	if err != nil || retained.CompletionProposal == nil || !reflect.DeepEqual(*retained.CompletionProposal, proposal) {
		t.Fatal("a reader changed the saved proposal")
	}

	_, completeErr := r.Complete(t.Context(), commands.CompleteOperationCommand{
		OperationID: op.ID, Output: output, CompletedBy: operations.CompletionModel,
	})

	if !errors.Is(completeErr, operations.ErrCompletionOperatorRequired) || reports.Count() != 0 {
		t.Fatal("a saved model proposal granted completion authority")
	}
	fixtures.CloseRunner(t, r)
	catalog.Change(func(m *missions.Mission) { m.Completion.DecisionBy = missions.Model })
	restarted := fixtures.NewRunner(t, config, fakes.Executor{})
	restored, err := restarted.Get(t.Context(), op.ID)
	if err != nil || restored.CompletionProposal == nil || !reflect.DeepEqual(*restored.CompletionProposal, proposal) ||
		restored.Status.Terminal() || restored.Mission.Completion.DecisionBy != missions.Operator || reports.Count() != 0 {
		t.Fatalf("recovery lost the proposal or changed completion authority: %+v, %v", restored, err)
	}
	approved := json.RawMessage(`{"summary":"Reviewed and edited by the operator"}`)

	completed, err := restarted.Complete(t.Context(), commands.CompleteOperationCommand{
		OperationID: op.ID, Output: approved, CompletedBy: operations.CompletionOperator,
	})
	report, reportErr := restarted.GetReport(t.Context(), op.ID)

	if err != nil || reportErr != nil || completed.Status != operations.Completed || completed.CompletedBy != operations.CompletionOperator ||
		!bytes.Equal(report.Output, approved) || !reflect.DeepEqual(*completed.CompletionProposal, proposal) || reports.Count() != 1 {
		t.Fatalf("operator approval did not retain its chosen final output and original proposal: %v, %v", err, reportErr)
	}
}

func TestCompletionProposalRejectsIneligibleOutputWithoutWrites(t *testing.T) {
	for _, scenario := range []string{"active task", "pending question", "invalid output", "missing origin", "unknown origin"} {
		t.Run(scenario, func(t *testing.T) {
			config := fixtures.Config()
			config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
				m.OutputSchema = json.RawMessage(`{"type":"object","required":["summary"]}`)
			})
			reports := fakes.NewTerminalReportStore()
			config.ReportStore = reports
			exec, calls := fakes.ControlledExecutor()
			r := fixtures.NewRunner(t, config, exec)
			op := fixtures.Start(t, r)
			command := commands.ProposeCompletionCommand{OperationID: op.ID, Output: json.RawMessage(`{"summary":"Reviewed"}`), ProposedBy: operations.CompletionModel}
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
				command.ProposedBy = ""
			case "unknown origin":
				command.ProposedBy = "service"
			}
			before, err := r.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}

			_, proposalErr := r.ProposeCompletion(t.Context(), command)
			retained, err := r.Get(t.Context(), op.ID)

			if proposalErr == nil || target != nil && !errors.Is(proposalErr, target) || err != nil ||
				!reflect.DeepEqual(retained, before) || reports.Count() != 0 {
				t.Fatalf("invalid completion proposal changed operation state: %v, %v", proposalErr, err)
			}
		})
	}
}

func TestUncertainCompletionProposalRequiresRecoveryWithoutReplacement(t *testing.T) {
	config := fixtures.Config()
	reports := fakes.NewTerminalReportStore()
	config.ReportStore = reports
	config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore,
		AfterProposeCompletion: func(operations.Operation) error { return operations.ErrWriteUncertain }}
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
	command := commands.ProposeCompletionCommand{OperationID: op.ID, Output: json.RawMessage(`{"summary":"Review me"}`), ProposedBy: operations.CompletionModel}

	_, proposalErr := first.ProposeCompletion(t.Context(), command)
	_, retryErr := first.ProposeCompletion(t.Context(), command)
	closeErr := first.Close(t.Context())
	stored, err := first.Get(t.Context(), op.ID)

	if !errors.Is(proposalErr, operations.ErrWriteUncertain) || !errors.Is(retryErr, operations.ErrWriteUncertain) ||
		!errors.Is(closeErr, operations.ErrWriteUncertain) || err != nil || stored.CompletionProposal == nil || reports.Count() != 0 {
		t.Fatal("uncertain proposal was retried or lost its saved snapshot")
	}
	restarted := fixtures.NewRunner(t, config, fakes.Executor{})

	restored, err := restarted.Get(t.Context(), op.ID)

	if err != nil || restored.CompletionProposal == nil || !reflect.DeepEqual(restored.CompletionProposal, stored.CompletionProposal) ||
		restored.Status.Terminal() || reports.Count() != 0 {
		t.Fatalf("recovery replaced or approved an uncertain proposal: %+v, %v", restored, err)
	}
}

package runner_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestMissionCompletionPolicyValidation(t *testing.T) {
	mission := fixtures.Config().MissionStore.(*fakes.MissionStore).Snapshot()
	definition, err := json.Marshal(mission)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name, policy string
		decision     missions.Decision
		invalid      bool
	}{
		{name: "omitted"},
		{name: "null", policy: "null"},
		{name: "model", policy: `{"decision_by":"model"}`, decision: missions.Model},
		{name: "operator", policy: `{"decision_by":"operator"}`, decision: missions.Operator},
		{name: "empty policy", policy: `{}`, invalid: true},
		{name: "empty decision", policy: `{"decision_by":""}`, invalid: true},
		{name: "null decision", policy: `{"decision_by":null}`, invalid: true},
		{name: "unsupported decision", policy: `{"decision_by":"code"}`, invalid: true},
		{name: "case-sensitive decision", policy: `{"decision_by":"Operator"}`, invalid: true},
		{name: "untrimmed decision", policy: `{"decision_by":"operator "}`, invalid: true},
		{name: "action-scoped policy", policy: `{"decision_by":"operator","for_actions":["inspect"]}`, invalid: true},
		{name: "string policy", policy: `"operator"`, invalid: true},
		{name: "array policy", policy: `[]`, invalid: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(definition, &fields); err != nil {
				t.Fatal(err)
			}
			if scenario.policy != "" {
				fields["completion"] = json.RawMessage(scenario.policy)
			}
			input, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}

			loaded, err := missions.LoadMission(input)

			if scenario.invalid {
				if err == nil {
					t.Fatal("invalid completion policy was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario.decision != "" {
				if loaded.Completion == nil || loaded.Completion.DecisionBy != scenario.decision {
					t.Fatal("completion policy changed during decoding")
				}
				return
			}
			if loaded.Completion != nil {
				t.Fatal("an omitted completion policy became explicit")
			}
			roundTrip, err := json.Marshal(loaded)
			if err != nil {
				t.Fatal(err)
			}
			var saved map[string]json.RawMessage
			if json.Unmarshal(roundTrip, &saved) != nil {
				t.Fatal("mission did not encode as an object")
			}
			if _, exists := saved["completion"]; exists {
				t.Fatal("an absent completion policy was written to JSON")
			}
		})
	}
}

func TestOperatorCompletionRejectsModelBeforeSavingReport(t *testing.T) {
	config := fixtures.Config()
	catalog := config.MissionStore.(*fakes.MissionStore)
	catalog.Change(func(m *missions.Mission) {
		m.Completion = &missions.CompletionPolicy{DecisionBy: missions.Operator}
	})
	reports := fakes.NewTerminalReportStore()
	config.ReportStore = reports
	r := fixtures.NewRunner(t, config, fakes.Executor{})
	op := fixtures.Start(t, r)
	catalog.Change(func(m *missions.Mission) { m.Completion.DecisionBy = missions.Model })
	command := commands.CompleteOperationCommand{
		OperationID: op.ID, Output: json.RawMessage(`{"review":"done"}`), CompletedBy: operations.CompletionModel,
	}

	_, completeErr := r.Complete(t.Context(), command)
	retained, err := r.Get(t.Context(), op.ID)

	if !errors.Is(completeErr, operations.ErrCompletionOperatorRequired) || err != nil ||
		!reflect.DeepEqual(retained, op) || reports.Count() != 0 {
		t.Fatalf("model completion changed an operator-controlled operation or saved a report: %v, %v", completeErr, err)
	}
	command.CompletedBy = operations.CompletionOperator

	completed, err := r.Complete(t.Context(), command)
	report, reportErr := r.GetReport(t.Context(), op.ID)

	if err != nil || completed.Status != operations.Completed || completed.CompletedBy != operations.CompletionOperator ||
		reportErr != nil || report.ID != completed.ReportID || reports.Count() != 1 {
		t.Fatalf("operator could not complete the retained policy: %+v, %v, %v", completed, err, reportErr)
	}
}

func TestCompletionAllowsModelAndOperatorWithoutOperatorPolicy(t *testing.T) {
	for _, policy := range []missions.Decision{"", missions.Model} {
		for _, origin := range []operations.CompletionOrigin{operations.CompletionModel, operations.CompletionOperator} {
			t.Run("policy="+string(policy)+"/origin="+string(origin), func(t *testing.T) {
				config := fixtures.Config()
				if policy != "" {
					config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
						m.Completion = &missions.CompletionPolicy{DecisionBy: policy}
					})
				}
				r := fixtures.NewRunner(t, config, fakes.Executor{})
				op := fixtures.Start(t, r)

				completed, err := r.Complete(t.Context(), commands.CompleteOperationCommand{
					OperationID: op.ID, Output: json.RawMessage(`{}`), CompletedBy: origin,
				})

				if err != nil || completed.Status != operations.Completed || completed.CompletedBy != origin {
					t.Fatalf("completion rejected a valid origin or lost it: %+v, %v", completed, err)
				}
			})
		}
	}
}

func TestCompletionRequiresValidOrigin(t *testing.T) {
	config := fixtures.Config()
	reports := fakes.NewTerminalReportStore()
	config.ReportStore = reports
	r := fixtures.NewRunner(t, config, fakes.Executor{})
	op := fixtures.Start(t, r)
	for _, origin := range []operations.CompletionOrigin{"", "human", "Model", "operator "} {
		t.Run(string(origin), func(t *testing.T) {
			command := commands.CompleteOperationCommand{OperationID: op.ID, Output: json.RawMessage(`{}`), CompletedBy: origin}
			next := op.Clone()

			_, completeErr := r.Complete(t.Context(), command)
			domainErr := next.ValidateCompletion(command.Output, origin)
			retained, err := r.Get(t.Context(), op.ID)

			if completeErr == nil || domainErr == nil || err != nil ||
				!reflect.DeepEqual(next, op) || !reflect.DeepEqual(retained, op) || reports.Count() != 0 {
				t.Fatal("missing or unknown completion origin was accepted or changed state")
			}
		})
	}
}

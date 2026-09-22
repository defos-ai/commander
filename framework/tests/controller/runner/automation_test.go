package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"commander/framework/comms/clients/controller/commands"
	"commander/framework/controller/runner"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/executions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	provider "commander/providers/controller"
)

type automationExecutor struct {
	release <-chan struct{}
	failure bool
	outside bool
	empty   bool
}

func (e automationExecutor) RunAction(ctx context.Context, agentID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	output := actionmodel.ActionOutput{ActionName: call.ActionName, Output: json.RawMessage(`{}`)}
	if call.ActionName == "prepare" {
		output.NextActions = []actionmodel.NextAction{
			{ActionName: "chunk", AgentID: agentID, Input: json.RawMessage(`{"part":1}`)},
			{ActionName: "chunk", AgentID: agentID, Input: json.RawMessage(`{"part":2}`)},
		}
		return output, nil
	}
	if string(call.Input) == `{"part":2}` {
		select {
		case <-ctx.Done():
			return actionmodel.ActionOutput{}, ctx.Err()
		case <-e.release:
		}
		if e.failure {
			return actionmodel.ActionOutput{}, errors.New("fixture chunk failed")
		}
	}
	if e.empty {
		return output, nil
	}
	action := "analyze"
	if e.outside && string(call.Input) == `{"part":2}` {
		action = "unconfigured"
	}
	// No assigned agent: downstream tasks remain queued for inspection.
	output.NextActions = []actionmodel.NextAction{{ActionName: action, Input: call.Input}}
	return output, nil
}

func (e automationExecutor) RecoverAction(ctx context.Context, agentID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	return e.RunAction(ctx, agentID, call)
}

func automationConfig(t *testing.T) runner.Config {
	t.Helper()
	database, err := provider.OpenDatabase(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	storage, err := provider.OpenReportStorage(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	if err := database.MissionStore.Upsert(t.Context(), missions.Mission{Name: "automation", Objective: "Automation", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	return runner.Config{MissionStore: database.MissionStore, OperationStore: database.OperationStore, ReportStore: storage.ReportStore, ArtifactRestorer: artifactIndex{}}
}

func TestAutomationCollectionAndDecision(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		wait                                 missions.ContinueWhen
		model, restart, fail, outside, empty bool
	}{
		{name: "each continues independently", wait: missions.WhenEach},
		{name: "all continues automatically", wait: missions.WhenAll},
		{name: "all continues after restart", wait: missions.WhenAll, restart: true},
		{name: "all hands off once to model", wait: missions.WhenAll, model: true},
		{name: "failed group hands off to model", wait: missions.WhenAll, fail: true},
		{name: "unapproved group hands off to model", wait: missions.WhenAll, outside: true},
		{name: "empty group hands off to model", wait: missions.WhenAll, empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: two sibling chunks, with the second held until explicitly released.
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			release := make(chan struct{})
			executor := automationExecutor{release: release, failure: tc.fail, outside: tc.outside, empty: tc.empty}
			config := automationConfig(t)
			rules := map[string]missions.Automation{
				"prepare": {Run: []string{"chunk"}, Continue: tc.wait},
				"chunk":   {Run: []string{"analyze"}},
			}
			if tc.model {
				rules["chunk"] = missions.Automation{Decide: missions.Model}
			}
			setMissionAutomations(t, config, rules)
			r, err := runner.NewRunner(ctx, executor, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.Close(context.Background()) })
			op, err := r.Start(ctx, commands.StartOperationCommand{MissionName: "automation", Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			parent, err := r.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, AgentID: "fixture-agent-1234abcd", ActionName: "prepare"})
			if err != nil {
				t.Fatal(err)
			}
			parent, err = r.WaitTask(ctx, op.ID, parent.ID)
			if err != nil {
				t.Fatal(err)
			}
			children := parent.Continuation.CreatedTaskIDs
			if len(children) != 2 {
				t.Fatalf("expected two chunks: %+v", parent)
			}
			if _, err := r.WaitTask(ctx, op.ID, children[0]); err != nil {
				t.Fatal(err)
			}
			before, err := r.Get(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantBefore := 3
			if tc.wait == missions.WhenEach {
				wantBefore = 4
			}
			if len(before.Tasks) != wantBefore {
				t.Fatalf("incorrect collection boundary: got %d tasks, want %d", len(before.Tasks), wantBefore)
			}
			if tc.wait == missions.WhenAll && before.Tasks[0].Continuation.Status != operations.ContinuationWaiting {
				t.Fatal("group did not wait for all children")
			}

			// Act: optionally restart at the collection boundary, then finish the group.
			if tc.restart {
				// The catalog now requests model decisions. The running operation
				// must retain its original rules through restart.
				setMissionAutomations(t, config, map[string]missions.Automation{"chunk": {Decide: missions.Model}})
				if err := r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				r, err = runner.NewRunner(ctx, executor, config)
				if err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			if _, err := r.WaitTask(ctx, op.ID, children[1]); err != nil {
				t.Fatal(err)
			}
			after, err := config.OperationStore.Get(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}

			// Assert: either both automatic follow-ups or one model decision, never a partial group.
			model := tc.model || tc.fail || tc.outside || tc.empty
			if model {
				c := after.Tasks[0].Continuation
				if len(after.Tasks) != 3 || c.DecisionBy != operations.DecisionModel || c.Status != operations.ContinuationPending {
					t.Fatalf("expected one model decision: %+v", after)
				}
				wantProposals := 2
				if tc.fail {
					wantProposals = 1
				}
				if tc.empty {
					wantProposals = 0
				}
				if len(c.ProposedActions) != wantProposals {
					t.Fatalf("lost collected proposals: %+v", c)
				}
			} else {
				if len(after.Tasks) != 5 || after.Tasks[0].Continuation.Status != operations.ContinuationResolved {
					t.Fatalf("automatic group did not continue: %+v", after)
				}
				for i, childID := range children {
					child := after.Tasks[i+1]
					if child.ID != childID || len(child.Continuation.CreatedTaskIDs) != 1 {
						t.Fatalf("lost or duplicated child decision: %+v", child)
					}
				}
			}
		})
	}
}

// A lost result can be reconciled after the model already involved the operator.
type interruptedAutomation struct{ automationExecutor }

func (interruptedAutomation) RunAction(context.Context, string, actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	return actionmodel.ActionOutput{}, executions.ErrIndeterminate
}

func TestAutomationRecoveryPreservesOperatorHandoff(t *testing.T) {
	// Arrange: an interrupted automatic action, followed by an answered operator question.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	config := automationConfig(t)
	rules := map[string]missions.Automation{"prepare": {Run: []string{"chunk"}, Continue: missions.WhenAll}}
	setMissionAutomations(t, config, rules)
	executor := interruptedAutomation{}
	r, err := runner.NewRunner(ctx, executor, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	op, err := r.Start(ctx, commands.StartOperationCommand{MissionName: "automation", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	task, err := r.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, AgentID: "fixture-agent-1234abcd", ActionName: "prepare"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = r.WaitTask(ctx, op.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	q, err := r.Ask(ctx, commands.AskQuestionCommand{OperationID: op.ID, TaskID: task.ID, Version: task.Continuation.Version, Text: "Should this branch continue?"})
	if err != nil {
		t.Fatal(err)
	}
	answered, err := r.Answer(ctx, commands.AnswerQuestionCommand{OperationID: op.ID, QuestionID: q.ID, Answer: "Inspect the result before continuing"})
	if err != nil {
		t.Fatal(err)
	}

	// Act: restart and recover the successful result, including proposed children.
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = runner.NewRunner(ctx, executor, config)
	if err != nil {
		t.Fatal(err)
	}
	var recovered operations.Operation
	for {
		recovered, err = r.Get(ctx, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.Tasks[0].Status == operations.TaskSucceeded {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Assert: recovery updates evidence without overriding the model/operator decision.
	c := recovered.Tasks[0].Continuation
	if len(recovered.Tasks) != 1 || c.QuestionID != q.ID || c.DecisionBy != operations.DecisionModel || c.Status != operations.ContinuationPending || c.Version != answered.Tasks[0].Continuation.Version {
		t.Fatalf("recovery bypassed operator handoff: %+v", recovered)
	}
}

func setMissionAutomations(t *testing.T, config runner.Config, rules map[string]missions.Automation) {
	t.Helper()
	mission, err := config.MissionStore.Get(t.Context(), "automation")
	if err != nil {
		t.Fatal(err)
	}
	mission.Automations = rules
	if err := config.MissionStore.Upsert(t.Context(), mission); err != nil {
		t.Fatal(err)
	}
}

func TestAutomationIsScopedToMissionSnapshot(t *testing.T) {
	// Arrange: one runner serves an automatic mission and an unrelated manual mission.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	config := automationConfig(t)
	rules := map[string]missions.Automation{
		"prepare": {Run: []string{"chunk"}, Continue: missions.WhenAll},
		"chunk":   {Run: []string{"analyze"}},
	}
	setMissionAutomations(t, config, rules)
	manual, err := config.MissionStore.Get(ctx, "automation")
	if err != nil {
		t.Fatal(err)
	}
	manual.Name, manual.Automations = "manual", nil
	if err := config.MissionStore.Upsert(ctx, manual); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	close(release)
	r, err := runner.NewRunner(ctx, automationExecutor{release: release}, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	original, err := r.Start(ctx, commands.StartOperationCommand{MissionName: "automation", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	// Mutating a returned snapshot must not change the operation inside the runner.
	original.Mission.Automations["prepare"].Run[0] = "unexpected"
	delete(original.Mission.Automations, "chunk")

	// Act: edit the catalog, then execute the same source action in all three operations.
	setMissionAutomations(t, config, map[string]missions.Automation{"prepare": {Decide: missions.Model}})
	for _, tc := range []struct {
		name      string
		existing  string
		automatic bool
	}{
		{name: "automation", existing: original.ID, automatic: true},
		{name: "automation"},
		{name: "manual"},
	} {
		operationID := tc.existing
		if operationID == "" {
			op, err := r.Start(ctx, commands.StartOperationCommand{MissionName: tc.name, Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			operationID = op.ID
		}
		task, err := r.Assign(ctx, commands.AssignTaskCommand{OperationID: operationID, AgentID: "fixture-agent-1234abcd", ActionName: "prepare"})
		if err != nil {
			t.Fatal(err)
		}
		task, err = r.WaitTask(ctx, operationID, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if tc.automatic {
			for _, childID := range task.Continuation.CreatedTaskIDs {
				if _, err := r.WaitTask(ctx, operationID, childID); err != nil {
					t.Fatal(err)
				}
			}
		}
		saved, err := config.OperationStore.Get(ctx, operationID)
		if err != nil {
			t.Fatal(err)
		}
		// Assert: only the old snapshot automates; neither another mission nor a new run inherits its rules.
		if tc.automatic {
			if len(saved.Tasks) != 5 {
				t.Fatalf("original automation changed: %+v", saved)
			}
		} else if len(saved.Tasks) != 1 || task.Continuation.DecisionBy != operations.DecisionModel || task.Continuation.Status != operations.ContinuationPending {
			t.Fatalf("mission %q inherited automatic execution: %+v", tc.name, saved)
		}
	}
}

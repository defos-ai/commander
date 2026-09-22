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
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	provider "commander/providers/controller"
)

type simultaneousResults struct{ release <-chan struct{} }

func (e simultaneousResults) RunAction(ctx context.Context, agentID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	select {
	case <-e.release:
		return actionmodel.ActionOutput{ActionName: call.ActionName, Output: json.RawMessage(`{}`), NextActions: []actionmodel.NextAction{{ActionName: "follow-up", AgentID: agentID}}}, nil
	case <-ctx.Done():
		return actionmodel.ActionOutput{}, ctx.Err()
	}
}

func (simultaneousResults) RecoverAction(context.Context, string, actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	return actionmodel.ActionOutput{}, errors.New("unexpected recovery")
}

func TestConcurrentResultsKeepSeparateContinuations(t *testing.T) {
	// Arrange: two agents have outstanding work in the same persisted operation.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	database, err := provider.OpenDatabase(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	storage, err := provider.OpenReportStorage(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	mission := missions.Mission{Name: "review", Objective: "Review", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	if err := database.MissionStore.Upsert(ctx, mission); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	r, err := runner.NewRunner(ctx, simultaneousResults{release}, runner.Config{
		MissionStore: database.MissionStore, OperationStore: database.OperationStore,
		ReportStore: storage.ReportStore, ArtifactRestorer: artifactIndex{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	operation, err := r.Start(ctx, commands.StartOperationCommand{MissionName: mission.Name, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var tasks []operations.Task
	for _, agentID := range []string{"fixture-agent-1234abcd", "fixture-agent-5678abcd"} {
		task, err := r.Assign(ctx, commands.AssignTaskCommand{OperationID: operation.ID, AgentID: agentID, ActionName: "observe", Input: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}

	// Act: release both results together and wait for both durable updates.
	close(release)
	for _, task := range tasks {
		if _, err := r.WaitTask(ctx, operation.ID, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := database.OperationStore.Get(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Assert: neither update overwrote the other task's result or proposal.
	if len(saved.Tasks) != 2 {
		t.Fatalf("unexpected tasks: %+v", saved.Tasks)
	}
	for _, task := range saved.Tasks {
		c := task.Continuation
		if task.Status != operations.TaskSucceeded || c == nil || c.Status != operations.ContinuationPending || len(c.ProposedActions) != 1 || c.ProposedActions[0].AgentID != task.AgentID {
			t.Fatalf("lost or mixed continuation: task=%+v continuation=%+v", task, c)
		}
	}
}

func TestConcurrentResolutionCreatesChildrenOnce(t *testing.T) {
	// Arrange: one pending model decision in a real persisted operation.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	database, err := provider.OpenDatabase(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	storage, err := provider.OpenReportStorage(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	mission := missions.Mission{Name: "review", Objective: "Review", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	if err := database.MissionStore.Upsert(ctx, mission); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	close(release)
	r, err := runner.NewRunner(ctx, simultaneousResults{release}, runner.Config{MissionStore: database.MissionStore, OperationStore: database.OperationStore, ReportStore: storage.ReportStore, ArtifactRestorer: artifactIndex{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	op, err := r.Start(ctx, commands.StartOperationCommand{MissionName: mission.Name, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	task, err := r.Assign(ctx, commands.AssignTaskCommand{OperationID: op.ID, AgentID: "fixture-agent-1234abcd", ActionName: "observe"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = r.WaitTask(ctx, op.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	command := commands.ResolveContinuationCommand{OperationID: op.ID, TaskID: task.ID, Version: task.Continuation.Version, Actions: []actionmodel.NextAction{{ActionName: "follow-up", Input: json.RawMessage(`{}`)}}}

	// Act: two model calls race using the same observed continuation version.
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := r.Resolve(ctx, command); results <- err }()
	}
	close(start)
	successes := 0
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	saved, err := database.OperationStore.Get(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Assert: the decision and exactly one queued child were saved together.
	if successes != 1 || len(saved.Tasks) != 2 || saved.Tasks[1].Status != operations.TaskQueued || saved.Tasks[1].ParentTaskID != task.ID || saved.Tasks[0].Continuation.Status != operations.ContinuationResolved || len(saved.Tasks[0].Continuation.CreatedTaskIDs) != 1 || saved.Tasks[0].Continuation.CreatedTaskIDs[0] != saved.Tasks[1].ID {
		t.Fatalf("duplicate or partial resolution: successes=%d operation=%+v", successes, saved)
	}
	// Operator intervention pauses queued work and is visible in operation status.
	question, err := r.Ask(ctx, commands.AskQuestionCommand{OperationID: op.ID, Text: "Which agent should run this?"})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := r.Get(ctx, op.ID)
	if err != nil || waiting.Status != operations.Waiting {
		t.Fatalf("queued question not waiting: %+v %v", waiting, err)
	}
	answered, err := r.Answer(ctx, commands.AnswerQuestionCommand{OperationID: op.ID, QuestionID: question.ID, Answer: "Cancel this operation"})
	if err != nil || len(answered.Tasks) != 2 || answered.Tasks[1].Status != operations.TaskQueued {
		t.Fatal("operator answer executed queued work")
	}
	// Queued work without a matching agent stays durable and can be cancelled.
	cancelled, err := r.Cancel(ctx, commands.CancelOperationCommand{OperationID: op.ID})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Tasks[1].Status != operations.TaskCancelled {
		t.Fatal("cancel left queued work dispatchable")
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	restarted, err := runner.NewRunner(ctx, simultaneousResults{release}, runner.Config{MissionStore: database.MissionStore, OperationStore: database.OperationStore, ReportStore: storage.ReportStore, ArtifactRestorer: artifactIndex{}})
	if err != nil {
		t.Fatalf("cannot recover cancelled unassigned task: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close(context.Background()) })

}

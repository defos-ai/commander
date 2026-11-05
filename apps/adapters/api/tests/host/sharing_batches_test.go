package api_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func TestSharedDataBatchBoundariesFollowRoutedChildrenAndWholeGroupSettlement(t *testing.T) {
	stack := hostfixtures.NewSharingStack(t)
	planner := stack.StartBatchPlanner(t)
	mission := hostfixtures.SharingMission("shared-finding-batches", []models.MissionSharingPolicyRequest{
		{FromActions: []string{fakes.FindingAction}, ToActions: []string{fakes.AnalysisAction}, Include: []string{"results", "artifacts"}},
		{FromActions: []string{fakes.FindingAction}, ToActions: []string{fakes.BatchAction}, Include: []string{"results"}},
	})
	mission.Policies.Continuation = &models.MissionContinuationPolicyRequest{AggregateResults: []string{fakes.BatchAction}}
	decodeSharing[models.MissionResponse](t, stack.Request(t, http.MethodPost, "/api/v1/missions", mission), http.StatusCreated)
	operation := startSharingOperation(t, stack, mission.Name)
	analysis := assignSharingTask(t, stack, operation.ID, fakes.AnalysisAction, json.RawMessage(`{}`))
	analysis = stack.AwaitWorking(t, operation.ID, analysis.ID)
	unmatched := assignSharingTask(t, stack, operation.ID, fakes.EvidenceAction, json.RawMessage(`{}`))
	unmatched = stack.AwaitWorking(t, operation.ID, unmatched.ID)
	other := startSharingOperation(t, stack, mission.Name)
	otherAnalysis := assignSharingTask(t, stack, other.ID, fakes.AnalysisAction, json.RawMessage(`{}`))
	otherAnalysis = stack.AwaitWorking(t, other.ID, otherAnalysis.ID)
	parent := assignSharingTask(t, stack, operation.ID, fakes.BatchAction, json.RawMessage(`{}`))
	parent = stack.AwaitTask(t, operation.ID, parent.ID, "succeeded")
	if parent.Continuation == nil {
		t.Fatal("planner has no continuation to create a producer batch")
	}

	accepted := decodeSharing[models.OperationResponse](t, stack.Request(t, http.MethodPost,
		"/api/v1/operations/"+operation.ID+"/tasks/"+parent.ID+"/continuation/resolve", models.ResolveContinuationRequest{
			Version: parent.Continuation.Version,
			Actions: []models.NextActionRequest{
				{ActionName: fakes.FindingAction, Input: json.RawMessage(`{"marker":"batch-finding"}`)},
				{ActionName: fakes.FindingAction, Input: json.RawMessage(`{"marker":"failed-finding","fail":true}`)},
				{ActionName: fakes.BatchAction, Input: json.RawMessage(`{"wait":true}`)},
			},
		}), http.StatusOK)
	var producers []models.TaskResponse
	var held models.TaskResponse
	for _, task := range accepted.Tasks {
		if task.ParentTaskID != parent.ID {
			continue
		}
		if task.ActionName == fakes.FindingAction {
			producers = append(producers, task)
		} else {
			held = task
		}
	}
	if len(producers) != 2 || held.ID == "" || !id.Valid(producers[0].BatchID) ||
		producers[1].BatchID != producers[0].BatchID || held.BatchID != producers[0].BatchID {
		t.Fatalf("accepted producer group lost membership: %+v", accepted.Tasks)
	}
	batchID := producers[0].BatchID
	members := []string{producers[0].ID, producers[1].ID}
	assertSharedBatch(t, receiveSharedFinding(t, stack, analysis.AgentID), parent, analysis, batchID, operations.TaskBatchStarted, members, nil)
	producers[0] = stack.AwaitTask(t, operation.ID, producers[0].ID, "succeeded")
	producers[1] = stack.AwaitTask(t, operation.ID, producers[1].ID, "failed")
	finding := receiveSharedFinding(t, stack, analysis.AgentID)
	assertSharedFinding(t, finding, producers[0], analysis, true, true, "batch-finding")
	if finding.Update.BatchID != batchID {
		t.Fatalf("shared producer result lost its batch identity: %+v", finding.Update)
	}
	awaitSharingGate(t, stack, planner.Gate())
	stack.AwaitWorking(t, operation.ID, held.ID)
	select {
	case premature := <-stack.Analyzer(analysis.AgentID).Received():
		t.Fatalf("batch finished before its unrouted child settled: %+v", premature.Update)
	default:
	}

	planner.Gate().Release()
	stack.AwaitTask(t, operation.ID, held.ID, "succeeded")
	assertSharedBatch(t, receiveSharedFinding(t, stack, analysis.AgentID), parent, analysis, batchID, operations.TaskBatchFinished, members,
		map[string]operations.TaskStatus{producers[0].ID: operations.TaskSucceeded, producers[1].ID: operations.TaskFailed})

	for _, message := range stack.Bus.SharedMessages() {
		if message.OperationID != operation.ID || message.AgentID != analysis.AgentID || message.TaskID != analysis.ID {
			t.Fatalf("batch sharing escaped its operation or action route: %+v", message)
		}
	}
	for _, recipient := range []models.TaskResponse{unmatched, otherAnalysis} {
		select {
		case unexpected := <-stack.Analyzer(recipient.AgentID).Received():
			t.Fatalf("unmatched receiver received batch data: %+v", unexpected.Update)
		default:
		}
	}
	stack.StopAnalyzers()
	for _, recipient := range []models.TaskResponse{analysis, unmatched, otherAnalysis} {
		finished := stack.AwaitTask(t, recipient.OperationID, recipient.ID, "succeeded")
		want := `{"findings":0}`
		if recipient.ID == analysis.ID {
			want = `{"findings":1}`
		}
		if finished.Result == nil || string(finished.Result.Output) != want {
			t.Fatalf("analyzer counted lifecycle signals or missed a finding: %+v; want %s", finished.Result, want)
		}
	}
}

func TestSharedDataBatchWaitsForTheOriginalInterruptedProducerOutcome(t *testing.T) {
	stack := hostfixtures.NewSharingStack(t)
	stack.StartBatchPlanner(t)
	mission := hostfixtures.SharingMission("shared-late-batch-result", []models.MissionSharingPolicyRequest{
		{FromActions: []string{fakes.FindingAction}, ToActions: []string{fakes.AnalysisAction}, Include: []string{"results"}},
	})
	mission.Policies.Continuation = &models.MissionContinuationPolicyRequest{AggregateResults: []string{fakes.BatchAction}}
	decodeSharing[models.MissionResponse](t, stack.Request(t, http.MethodPost, "/api/v1/missions", mission), http.StatusCreated)
	operation := startSharingOperation(t, stack, mission.Name)
	analysis := assignSharingTask(t, stack, operation.ID, fakes.AnalysisAction, json.RawMessage(`{}`))
	analysis = stack.AwaitWorking(t, operation.ID, analysis.ID)
	parent := assignSharingTask(t, stack, operation.ID, fakes.BatchAction, json.RawMessage(`{}`))
	parent = stack.AwaitTask(t, operation.ID, parent.ID, "succeeded")
	if parent.Continuation == nil {
		t.Fatal("planner has no continuation to create a producer batch")
	}
	held := stack.Producer.BlockNextRun()
	stack.Bus.LoseNextActionConfirmation(fakes.FindingAction)

	accepted := decodeSharing[models.OperationResponse](t, stack.Request(t, http.MethodPost,
		"/api/v1/operations/"+operation.ID+"/tasks/"+parent.ID+"/continuation/resolve", models.ResolveContinuationRequest{
			Version: parent.Continuation.Version,
			Actions: []models.NextActionRequest{{ActionName: fakes.FindingAction, Input: json.RawMessage(`{"marker":"late-finding"}`)}},
		}), http.StatusOK)
	var producer models.TaskResponse
	for _, task := range accepted.Tasks {
		if task.ParentTaskID == parent.ID {
			producer = task
		}
	}
	if !id.Valid(producer.ID) || !id.Valid(producer.BatchID) {
		t.Fatalf("accepted producer lost batch membership: %+v", accepted.Tasks)
	}
	batchID := producer.BatchID
	members := []string{producer.ID}
	assertSharedBatch(t, receiveSharedFinding(t, stack, analysis.AgentID), parent, analysis, batchID, operations.TaskBatchStarted, members, nil)
	awaitSharingGate(t, stack, held)
	interrupted := stack.AwaitTask(t, operation.ID, producer.ID, "interrupted")
	current := decodeSharing[models.OperationResponse](t, stack.Request(t, http.MethodGet, "/api/v1/operations/"+operation.ID, nil), http.StatusOK)
	for _, event := range current.Events {
		if event.BatchID == batchID && event.Type == string(operations.TaskBatchFinished) {
			t.Fatalf("unknown producer outcome closed its batch: %+v", event)
		}
	}
	select {
	case premature := <-stack.Analyzer(analysis.AgentID).Received():
		t.Fatalf("unknown execution produced a result or finish boundary: %+v", premature.Update)
	default:
	}

	held.Release()
	producer = stack.AwaitReconciledTask(t, operation.ID, producer.ID, "succeeded")
	finding := receiveSharedFinding(t, stack, analysis.AgentID)
	assertSharedFinding(t, finding, producer, analysis, true, false, "late-finding")
	assertSharedBatch(t, receiveSharedFinding(t, stack, analysis.AgentID), parent, analysis, batchID, operations.TaskBatchFinished, members,
		map[string]operations.TaskStatus{producer.ID: operations.TaskSucceeded})

	if producer.BatchID != batchID || finding.Update.BatchID != batchID || producer.AgentID != interrupted.AgentID ||
		producer.CommandID != interrupted.CommandID || stack.Producer.Calls() != 1 {
		t.Fatalf("late reconciliation changed batch identity or repeated the producer: before=%+v after=%+v calls=%d", interrupted, producer, stack.Producer.Calls())
	}
	stack.StopAnalyzers()
	stack.AwaitTask(t, operation.ID, analysis.ID, "succeeded")
}

func assertSharedBatch(t *testing.T, finding fakes.SharedFinding, parent, recipient models.TaskResponse, batchID string,
	kind operations.EventType, members []string, outcomes map[string]operations.TaskStatus) {
	t.Helper()
	update := finding.Update
	if !id.ValidRuntime(update.Source.ControllerID, id.Controller) || update.Source.AgentID != "" || update.Source.CommandID != "" || update.Source.ActionName != "" ||
		update.Source.OperationID != parent.OperationID || update.Source.TaskID != parent.ID || update.BatchID != batchID ||
		finding.Execution.OperationID != recipient.OperationID || finding.Execution.TaskID != recipient.ID || finding.Execution.CommandID != recipient.CommandID {
		t.Fatalf("batch boundary lost controller origin or recipient identity: %+v", finding)
	}
	if update.Batch == nil || update.Batch.Type != kind || !slices.Equal(update.Batch.TaskIDs, members) || !maps.Equal(update.Batch.Outcomes, outcomes) ||
		len(update.Result) != 0 || len(update.Artifacts) != 0 || update.Decision != nil || len(finding.Files) != 0 {
		t.Fatalf("batch boundary did not retain its routed membership and known outcomes: %+v", update)
	}
}

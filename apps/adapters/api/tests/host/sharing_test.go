package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
)

func TestSharedDataUsesSavedPoliciesAndAcceptedEvidence(t *testing.T) {
	stack := hostfixtures.NewSharingStack(t)
	mission := hostfixtures.SharingMission("shared-findings", []models.MissionSharingPolicyRequest{
		{FromActions: []string{fakes.FindingAction}, ToActions: []string{fakes.AnalysisAction}, Include: []string{"results"}},
		{FromActions: []string{fakes.FindingAction}, ToActions: []string{fakes.AnalysisAction, fakes.EvidenceAction}, Include: []string{"artifacts"}},
	})
	decodeSharing[models.MissionResponse](t, stack.Request(t, http.MethodPost, "/api/v1/missions", mission), http.StatusCreated)
	operation := startSharingOperation(t, stack, mission.Name)
	analysis := assignSharingTask(t, stack, operation.ID, fakes.AnalysisAction, json.RawMessage(`{}`))
	analysis = stack.AwaitWorking(t, operation.ID, analysis.ID)
	evidence := assignSharingTask(t, stack, operation.ID, fakes.EvidenceAction, json.RawMessage(`{}`))
	evidence = stack.AwaitWorking(t, operation.ID, evidence.ID)

	mission.Policies = nil
	decodeSharing[models.MissionResponse](t, stack.Request(t, http.MethodPut, "/api/v1/missions/"+mission.Name, mission), http.StatusOK)
	unsharedOperation := startSharingOperation(t, stack, mission.Name)
	unsharedAnalysis := assignSharingTask(t, stack, unsharedOperation.ID, fakes.AnalysisAction, json.RawMessage(`{}`))
	unsharedAnalysis = stack.AwaitWorking(t, unsharedOperation.ID, unsharedAnalysis.ID)
	ignored := assignSharingTask(t, stack, unsharedOperation.ID, fakes.FindingAction, json.RawMessage(`{"marker":"unshared"}`))
	stack.AwaitTask(t, unsharedOperation.ID, ignored.ID, "succeeded")
	first := assignSharingTask(t, stack, operation.ID, fakes.FindingAction, json.RawMessage(`{"marker":"first"}`))
	first = stack.AwaitTask(t, operation.ID, first.ID, "succeeded")

	assertSharedFinding(t, receiveSharedFinding(t, stack, analysis.AgentID), first, analysis, true, true, "first")
	assertSharedFinding(t, receiveSharedFinding(t, stack, evidence.AgentID), first, evidence, false, true, "first")
	stack.AwaitTask(t, operation.ID, analysis.ID, "running")
	stack.AwaitTask(t, operation.ID, evidence.ID, "running")
	if operation.Mission.Policies == nil || unsharedOperation.Mission.Policies != nil {
		t.Fatal("operations did not retain their different mission policy snapshots")
	}

	second := assignSharingTask(t, stack, operation.ID, fakes.FindingAction, json.RawMessage(`{"marker":"second"}`))
	second = stack.AwaitTask(t, operation.ID, second.ID, "succeeded")

	assertSharedFinding(t, receiveSharedFinding(t, stack, analysis.AgentID), second, analysis, true, true, "second")
	assertSharedFinding(t, receiveSharedFinding(t, stack, evidence.AgentID), second, evidence, false, true, "second")
	// The later source drains earlier jobs; accepted result replays may still
	// repeat the same source/recipient update, so count execution pairs.
	shared := stack.Bus.SharedMessages()
	if pairs := countSharedPairs(t, shared); pairs != 4 {
		t.Fatalf("sharing skipped a recipient or shared disabled work: got %d execution pairs", pairs)
	}
	for _, message := range shared {
		if message.Work.OperationID != operation.ID || message.AgentID == unsharedAnalysis.AgentID {
			t.Fatalf("shared evidence escaped its operation: %+v", message)
		}
	}
	select {
	case unexpected := <-stack.Analyzer(unsharedAnalysis.AgentID).Received():
		t.Fatalf("operation without a sharing policy received evidence: %+v", unexpected)
	default:
	}
	stack.StopAnalyzers()
	for _, task := range []models.TaskResponse{analysis, evidence, unsharedAnalysis} {
		stack.AwaitTask(t, task.OperationID, task.ID, "succeeded")
	}
}

func TestSharedDataFailureDoesNotBlockAcceptedWork(t *testing.T) {
	stack := hostfixtures.NewSharingStack(t)
	mission := hostfixtures.SharingMission("sharing-failure", []models.MissionSharingPolicyRequest{
		{FromActions: []string{fakes.FindingAction}, ToActions: []string{fakes.AnalysisAction}, Include: []string{"results"}},
	})
	decodeSharing[models.MissionResponse](t, stack.Request(t, http.MethodPost, "/api/v1/missions", mission), http.StatusCreated)
	operation := startSharingOperation(t, stack, mission.Name)
	analysis := assignSharingTask(t, stack, operation.ID, fakes.AnalysisAction, json.RawMessage(`{}`))
	analysis = stack.AwaitWorking(t, operation.ID, analysis.ID)
	blocked := stack.Bus.BlockNextSharedData()

	first := assignSharingTask(t, stack, operation.ID, fakes.FindingAction, json.RawMessage(`{"marker":"transport-failed"}`))
	first = stack.AwaitTask(t, operation.ID, first.ID, "succeeded")
	awaitSharingGate(t, stack, blocked)
	second := assignSharingTask(t, stack, operation.ID, fakes.FindingAction, json.RawMessage(`{"marker":"after-failure"}`))
	second = stack.AwaitTask(t, operation.ID, second.ID, "succeeded")

	if stack.Producer.Calls() != 2 || stack.Controller.Err() != nil {
		t.Fatal("shared-data publication blocked controller command or result processing")
	}
	blocked.Release()
	assertSharedFinding(t, receiveSharedFinding(t, stack, analysis.AgentID), second, analysis, true, false, "after-failure")
	retained := stack.AwaitTask(t, operation.ID, first.ID, "succeeded")
	if retained.Result == nil || len(retained.Result.Artifacts) != 1 || !bytes.Equal(retained.Result.Output, first.Result.Output) {
		t.Fatal("best-effort sharing failure changed the accepted producer outcome")
	}
	if got := countSharedPairs(t, stack.Bus.SharedMessages()); got != 2 {
		t.Fatalf("shared-data failure skipped or misrouted a source: %d execution pairs", got)
	}
	stack.StopAnalyzers()
	finished := stack.AwaitTask(t, operation.ID, analysis.ID, "succeeded")
	if finished.Result == nil || string(finished.Result.Output) != `{"findings":1}` {
		t.Fatalf("analyzer did not finish after receiving only the successful publication: %+v", finished.Result)
	}
}

func TestSharedDataRechecksCancellationAfterReadingArtifacts(t *testing.T) {
	stack := hostfixtures.NewSharingStack(t)
	mission := hostfixtures.SharingMission("sharing-cancelled", []models.MissionSharingPolicyRequest{
		{FromActions: []string{fakes.FindingAction}, ToActions: []string{fakes.AnalysisAction}, Include: []string{"artifacts"}},
	})
	decodeSharing[models.MissionResponse](t, stack.Request(t, http.MethodPost, "/api/v1/missions", mission), http.StatusCreated)
	operation := startSharingOperation(t, stack, mission.Name)
	analysis := assignSharingTask(t, stack, operation.ID, fakes.AnalysisAction, json.RawMessage(`{}`))
	analysis = stack.AwaitWorking(t, operation.ID, analysis.ID)
	blocked := stack.Artifacts.BlockNextRead()

	first := assignSharingTask(t, stack, operation.ID, fakes.FindingAction, json.RawMessage(`{"marker":"cancelled"}`))
	stack.AwaitTask(t, operation.ID, first.ID, "succeeded")
	awaitSharingGate(t, stack, blocked)
	decodeSharing[models.OperationResponse](t, stack.Request(t, http.MethodPost, "/api/v1/operations/"+operation.ID+"/cancel", nil), http.StatusOK)
	active := startSharingOperation(t, stack, mission.Name)
	activeAnalysis := assignSharingTask(t, stack, active.ID, fakes.AnalysisAction, json.RawMessage(`{}`))
	activeAnalysis = stack.AwaitWorking(t, active.ID, activeAnalysis.ID)
	blocked.Release()
	barrier := assignSharingTask(t, stack, active.ID, fakes.FindingAction, json.RawMessage(`{"marker":"active"}`))
	barrier = stack.AwaitTask(t, active.ID, barrier.ID, "succeeded")

	assertSharedFinding(t, receiveSharedFinding(t, stack, activeAnalysis.AgentID), barrier, activeAnalysis, false, true, "active")
	shared := stack.Bus.SharedMessages()
	if countSharedPairs(t, shared) != 1 {
		t.Fatalf("cancelled operation forwarded stale evidence after readback: %+v", shared)
	}
	for _, message := range shared {
		if message.Work.OperationID != active.ID {
			t.Fatalf("cancelled operation forwarded stale evidence after readback: %+v", message)
		}
	}
	select {
	case update := <-stack.Analyzer(analysis.AgentID).Received():
		t.Fatalf("cancelled recipient received evidence: %+v", update)
	default:
	}
	stack.StopAnalyzers()
	stack.AwaitTask(t, active.ID, activeAnalysis.ID, "succeeded")
}

func startSharingOperation(t *testing.T, stack *hostfixtures.SharingStack, mission string) models.OperationResponse {
	t.Helper()
	return decodeSharing[models.OperationResponse](t, stack.Request(t, http.MethodPost, "/api/v1/operations", models.StartOperationRequest{
		MissionName: mission, Input: json.RawMessage(`{}`),
	}), http.StatusCreated)
}

func assignSharingTask(t *testing.T, stack *hostfixtures.SharingStack, operationID, action string, input json.RawMessage) models.TaskResponse {
	t.Helper()
	return decodeSharing[models.TaskResponse](t, stack.Request(t, http.MethodPost, "/api/v1/operations/"+operationID+"/tasks", models.AssignTaskRequest{
		ActionName: action, Input: input,
	}), http.StatusCreated)
}

func receiveSharedFinding(t *testing.T, stack *hostfixtures.SharingStack, agentID string) fakes.SharedFinding {
	t.Helper()
	select {
	case finding := <-stack.Analyzer(agentID).Received():
		return finding
	case <-stack.Context.Done():
		t.Fatalf("analyzer %s did not receive accepted evidence: %v; controller: %v", agentID, stack.Context.Err(), stack.Controller.Err())
		return fakes.SharedFinding{}
	}
}

func awaitSharingGate(t *testing.T, stack *hostfixtures.SharingStack, gate *fakes.SharingGate) {
	t.Helper()
	select {
	case <-gate.Entered():
	case <-stack.Context.Done():
		t.Fatal("sharing did not reach the injected transport/readback gate")
	}
}

func decodeSharing[T any](t *testing.T, response hostfixtures.Response, status int) T {
	t.Helper()
	if response.Status != status {
		t.Fatalf("API returned %d, want %d: %s", response.Status, status, response.Body)
	}
	var value T
	if err := json.Unmarshal([]byte(response.Body), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func countSharedPairs(t *testing.T, shared []messages.Message) int {
	t.Helper()
	pairs := make(map[[4]string][]byte)
	for _, message := range shared {
		update, err := agentevents.DecodeContextUpdate(message)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		key := [4]string{update.Source.AgentID, update.Source.CommandID, message.AgentID, message.CorrelationID}
		if previous, exists := pairs[key]; exists && !bytes.Equal(previous, payload) {
			t.Fatalf("repeated execution pair changed its combined sharing payload: %s != %s", previous, payload)
		}
		pairs[key] = payload
	}
	return len(pairs)
}

func assertSharedFinding(t *testing.T, finding fakes.SharedFinding, source, target models.TaskResponse, results, artifacts bool, marker string) {
	t.Helper()
	update := finding.Update
	if update.Source.AgentID != source.AgentID || update.Source.CommandID != source.CommandID || update.Source.ActionName != source.ActionName ||
		update.Source.Work.OperationID != source.OperationID || update.Source.Work.TaskID != source.ID ||
		finding.Execution.CommandID != target.CommandID || finding.Execution.Work.OperationID != target.OperationID || finding.Execution.Work.TaskID != target.ID {
		t.Fatalf("forwarding lost source or recipient execution provenance: %+v; recipient: %+v", update.Source, finding.Execution)
	}
	if update.Decision != nil || source.Result == nil {
		t.Fatal("result/artifact sharing forwarded a decision or has no accepted source result")
	}
	if results {
		if !bytes.Equal(update.Result, source.Result.Output) {
			t.Fatalf("shared result differs from saved output: %s != %s", update.Result, source.Result.Output)
		}
		var output struct {
			Marker  string `json:"marker"`
			Finding int64  `json:"finding"`
		}
		if err := json.Unmarshal(update.Result, &output); err != nil || output.Marker != marker || output.Finding != 9007199254740993 {
			t.Fatalf("shared result was duplicated, reordered or lost integer precision: %s", update.Result)
		}
	} else if len(update.Result) != 0 {
		t.Fatal("artifacts-only route exposed a source result")
	}
	if !artifacts {
		if len(update.Artifacts) != 0 || len(finding.Files) != 0 {
			t.Fatal("results-only route exposed source artifacts")
		}
		return
	}
	if len(update.Artifacts) != 1 || len(source.Result.Artifacts) != 1 {
		t.Fatalf("artifact sharing lost or duplicated accepted metadata: %+v", update.Artifacts)
	}
	shared, saved := update.Artifacts[0], source.Result.Artifacts[0]
	if shared.ID != saved.ID || shared.Name != saved.Name || shared.AgentID != saved.AgentID || shared.CommandID != saved.CommandID ||
		shared.ActionName != saved.ActionName || !shared.CreatedAt.Equal(saved.CreatedAt) || len(shared.Files) != 1 || len(saved.Files) != 1 {
		t.Fatalf("forwarded metadata differs from the accepted artifact: %+v != %+v", shared, saved)
	}
	file, accepted := shared.Files[0], saved.Files[0]
	want := []byte("evidence:" + marker)
	digest := sha256.Sum256(want)
	if file.ID != accepted.ID || file.Name != accepted.Name || file.MediaType != accepted.MediaType || file.Size != accepted.Size ||
		file.SHA256 != accepted.SHA256 || file.Size != int64(len(want)) || file.SHA256 != hex.EncodeToString(digest[:]) ||
		len(finding.Files) != 1 || !bytes.Equal(finding.Files[file.ID], want) {
		t.Fatalf("analyzer could not retrieve the accepted artifact with its saved checksum: %+v; bytes=%q", file, finding.Files[file.ID])
	}
}

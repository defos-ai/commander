package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/operations"
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
		if message.OperationID != operation.ID || message.AgentID == unsharedAnalysis.AgentID {
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
		if message.OperationID != active.ID {
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

func TestSharedDataForwardsSavedTaskDecisions(t *testing.T) {
	stack := hostfixtures.NewSharingStack(t)
	mission := hostfixtures.SharingMission("shared-decisions", []models.MissionSharingPolicyRequest{
		{FromActions: []string{fakes.FindingAction}, ToActions: []string{fakes.AnalysisAction}, Include: []string{"decisions"}},
		{FromActions: []string{fakes.FindingAction}, ToActions: []string{fakes.EvidenceAction}, Include: []string{"results"}},
	})
	decodeSharing[models.MissionResponse](t, stack.Request(t, http.MethodPost, "/api/v1/missions", mission), http.StatusCreated)
	operation := startSharingOperation(t, stack, mission.Name)
	analysis := assignSharingTask(t, stack, operation.ID, fakes.AnalysisAction, json.RawMessage(`{}`))
	analysis = stack.AwaitWorking(t, operation.ID, analysis.ID)
	evidence := assignSharingTask(t, stack, operation.ID, fakes.EvidenceAction, json.RawMessage(`{}`))
	evidence = stack.AwaitWorking(t, operation.ID, evidence.ID)
	otherOperation := startSharingOperation(t, stack, mission.Name)
	otherAnalysis := assignSharingTask(t, stack, otherOperation.ID, fakes.AnalysisAction, json.RawMessage(`{}`))
	otherAnalysis = stack.AwaitWorking(t, otherOperation.ID, otherAnalysis.ID)
	producer := assignSharingTask(t, stack, operation.ID, fakes.FindingAction, json.RawMessage(`{"marker":"decisions","questions":true}`))
	producer = stack.AwaitWorking(t, operation.ID, producer.ID)

	for _, key := range []string{"scope", "priority"} {
		pending := stack.AwaitQuestion(t, operation.ID, key)
		decodeSharing[models.OperationResponse](t, stack.Request(t, http.MethodPost,
			"/api/v1/operations/"+operation.ID+"/questions/"+pending.ID+"/answer", models.AnswerQuestionRequest{Answer: "Investigate"}), http.StatusOK)
		saved := readSharingQuestion(t, stack, operation.ID, pending.ID)

		assertSharedDecision(t, receiveSharedFinding(t, stack, analysis.AgentID), producer, analysis, saved, "operator")
		if saved.CommandID != producer.CommandID || saved.Key != key {
			t.Fatal("execution question lost its original command and stable key")
		}
		duplicate := stack.Request(t, http.MethodPost, "/api/v1/operations/"+operation.ID+"/questions/"+pending.ID+"/answer",
			models.AnswerQuestionRequest{Answer: "Ignore"})
		if duplicate.Status != http.StatusConflict {
			t.Fatalf("answer replacement was accepted: %d %s", duplicate.Status, duplicate.Body)
		}
	}
	producer = stack.AwaitTask(t, operation.ID, producer.ID, "succeeded")
	assertSharedFinding(t, receiveSharedFinding(t, stack, evidence.AgentID), producer, evidence, true, false, "decisions")

	operationQuestion := decodeSharing[models.QuestionResponse](t, stack.Request(t, http.MethodPost,
		"/api/v1/operations/"+operation.ID+"/questions", models.AskQuestionRequest{Text: "Continue monitoring the whole operation?"}), http.StatusCreated)
	decodeSharing[models.OperationResponse](t, stack.Request(t, http.MethodPost,
		"/api/v1/operations/"+operation.ID+"/questions/"+operationQuestion.ID+"/answer", models.AnswerQuestionRequest{Answer: "Continue"}), http.StatusOK)
	if producer.Continuation == nil {
		t.Fatal("finished producer has no model continuation for a task-linked question")
	}
	modelQuestion := decodeSharing[models.QuestionResponse](t, stack.Request(t, http.MethodPost,
		"/api/v1/operations/"+operation.ID+"/questions", models.AskQuestionRequest{TaskID: producer.ID, Version: producer.Continuation.Version,
			Text: "Which evidence should the next analysis prioritize?", Options: []string{"Network", "Identity"}, TimeoutSeconds: 2}), http.StatusCreated)
	if err := stack.AnswerAsModel(operation.ID, modelQuestion.ID, "Network"); !errors.Is(err, operations.ErrQuestionOperatorWaiting) {
		t.Fatalf("model answer did not respect the operator-first window: %v", err)
	}
	deadline := time.NewTimer(time.Until(modelQuestion.FallbackAt))
	defer deadline.Stop()
	select {
	case <-deadline.C:
	case <-stack.Context.Done():
		t.Fatal("model fallback deadline exceeded the sharing fixture lifetime")
	}
	if err := stack.AnswerAsModel(operation.ID, modelQuestion.ID, "Identity"); err != nil {
		t.Fatal(err)
	}
	savedModel := readSharingQuestion(t, stack, operation.ID, modelQuestion.ID)

	assertSharedDecision(t, receiveSharedFinding(t, stack, analysis.AgentID), producer, analysis, savedModel, "model")
	shared := stack.Bus.SharedMessages()
	if pairs := countSharedPairs(t, shared); pairs != 4 {
		t.Fatalf("decision filtering lost a question, shared an operation-wide question or exposed a result: %d pairs", pairs)
	}
	for _, message := range shared {
		update, err := agentevents.DecodeSharedData(message)
		if err != nil {
			t.Fatal(err)
		}
		if message.OperationID != operation.ID || message.AgentID == otherAnalysis.AgentID ||
			update.Decision != nil && (update.Decision.ID == operationQuestion.ID || message.AgentID != analysis.AgentID) ||
			len(update.Result) != 0 && message.AgentID != evidence.AgentID {
			t.Fatalf("saved decision or result escaped its selected task scope: %+v", message)
		}
	}
	stack.StopAnalyzers()
	for _, entry := range []struct {
		task models.TaskResponse
		want string
	}{{analysis, `{"findings":3}`}, {evidence, `{"findings":1}`}, {otherAnalysis, `{"findings":0}`}} {
		finished := stack.AwaitTask(t, entry.task.OperationID, entry.task.ID, "succeeded")
		if finished.Result == nil || string(finished.Result.Output) != entry.want {
			t.Fatalf("analyzer received the wrong distinct decision/result set: %+v; want %s", finished.Result, entry.want)
		}
	}
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
	pairs := make(map[[5]string][]byte)
	for _, message := range shared {
		update, err := agentevents.DecodeSharedData(message)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		key := [5]string{update.Source.AgentID, update.Source.CommandID, message.AgentID, message.CorrelationID, ""}
		if update.Decision != nil {
			key[4] = update.Decision.ID
		}
		if previous, exists := pairs[key]; exists && !bytes.Equal(previous, payload) {
			t.Fatalf("repeated execution pair changed its combined sharing payload: %s != %s", previous, payload)
		}
		pairs[key] = payload
	}
	return len(pairs)
}

func readSharingQuestion(t *testing.T, stack *hostfixtures.SharingStack, operationID, questionID string) models.QuestionResponse {
	t.Helper()
	return decodeSharing[models.QuestionResponse](t, stack.Request(t, http.MethodGet,
		"/api/v1/operations/"+operationID+"/questions/"+questionID, nil), http.StatusOK)
}

func assertSharedDecision(t *testing.T, finding fakes.SharedFinding, source, target models.TaskResponse, saved models.QuestionResponse, origin string) {
	t.Helper()
	update := finding.Update
	if update.Source.AgentID != source.AgentID || update.Source.CommandID != source.CommandID || update.Source.ActionName != source.ActionName ||
		update.Source.OperationID != source.OperationID || update.Source.TaskID != source.ID ||
		finding.Execution.CommandID != target.CommandID || finding.Execution.OperationID != target.OperationID || finding.Execution.TaskID != target.ID {
		t.Fatalf("forwarding lost decision source or recipient provenance: %+v; recipient: %+v", update.Source, finding.Execution)
	}
	decision := update.Decision
	if decision == nil || len(update.Result) != 0 || len(update.Artifacts) != 0 || len(finding.Files) != 0 {
		t.Fatalf("decisions-only route exposed something besides its saved answer: %+v", update)
	}
	if saved.AnsweredBy != origin || decision.ID != saved.ID || string(decision.Status) != saved.Status || decision.TaskID != saved.TaskID ||
		decision.CommandID != saved.CommandID || decision.Key != saved.Key || decision.Text != saved.Text || !slices.Equal(decision.Options, saved.Options) ||
		decision.Answer != saved.Answer || string(decision.AnsweredBy) != saved.AnsweredBy || !decision.AskedAt.Equal(saved.AskedAt) ||
		!decision.FallbackAt.Equal(saved.FallbackAt) || decision.AnsweredAt == nil || saved.AnsweredAt == nil || !decision.AnsweredAt.Equal(*saved.AnsweredAt) {
		t.Fatalf("forwarded decision differs from the saved %s answer: %+v != %+v", origin, decision, saved)
	}
}

func assertSharedFinding(t *testing.T, finding fakes.SharedFinding, source, target models.TaskResponse, results, artifacts bool, marker string) {
	t.Helper()
	update := finding.Update
	if update.Source.AgentID != source.AgentID || update.Source.CommandID != source.CommandID || update.Source.ActionName != source.ActionName ||
		update.Source.OperationID != source.OperationID || update.Source.TaskID != source.ID ||
		finding.Execution.CommandID != target.CommandID || finding.Execution.OperationID != target.OperationID || finding.Execution.TaskID != target.ID {
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

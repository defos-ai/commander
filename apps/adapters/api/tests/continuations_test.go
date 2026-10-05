package api_test

import (
	"bytes"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
)

func TestOperationLinkedQuestionAnswerRequiresExplicitContinuationResolution(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), continuationOperationRecord(1))
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID
	parent := op.Tasks[0]
	resolvePath := base + "/tasks/" + parent.ID + "/continuation/resolve"
	askBody := fmt.Sprintf(`{"text":"Which evidence needs follow-up?","task_id":%q,"version":2}`, parent.ID)

	question := decodeResponse[operations.Question](t, server.request(t, http.MethodPost, base+"/questions", authenticate, commandBody(askBody, "application/json")), http.StatusCreated)
	waiting := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))

	continuation := waiting.Tasks[0].Continuation
	if question.TaskID != parent.ID || question.StepID != parent.StepID || len(waiting.Tasks) != 1 || continuation.Version != 3 || continuation.DecisionBy != operations.DecisionUser || continuation.Status != operations.ContinuationWaiting || continuation.QuestionID != question.ID {
		t.Fatalf("linked ask did not preserve task context and pause its decision: question=%+v operation=%+v", question, waiting)
	}
	before := operationBytes(t, fixture, op.ID)
	assertAPIError(t, server.request(t, http.MethodPost, base+"/questions", authenticate, commandBody(askBody, "application/json")), http.StatusConflict, "continuation_conflict")
	assertAPIError(t, server.request(t, http.MethodPost, resolvePath, authenticate, commandBody(`{"version":2,"actions":[]}`, "application/json")), http.StatusConflict, "continuation_conflict")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("stale decisions changed the waiting question or continuation")
	}
	answered := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, base+"/questions/"+question.ID+"/answer", authenticate, commandBody(`{"answer":"Review links and summarize the findings."}`, "application/json")))
	stored := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))

	continuation = answered.Tasks[0].Continuation
	if !reflect.DeepEqual(answered, stored) || len(answered.Tasks) != 1 || continuation.Version != 4 || continuation.DecisionBy != operations.DecisionModel || continuation.Status != operations.ContinuationPending || continuation.QuestionID != question.ID || len(continuation.CreatedTaskIDs) != 0 || !reflect.DeepEqual(continuation.ProposedActions, parent.Continuation.ProposedActions) {
		t.Fatalf("answer must return a pending model decision without creating children: %+v", answered)
	}
	body := `{"version":4,"actions":[{"action_name":"review","input":{"url":"https://example.test/links"},"reason":"Check the selected links"},{"action_name":"summarize"}]}`
	resolved := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, resolvePath, authenticate, commandBody(body, "application/json")))
	stored = decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))

	if !reflect.DeepEqual(resolved, stored) || len(resolved.Tasks) != 3 {
		t.Fatalf("resolve must atomically persist its decision and both children: response=%+v saved=%+v", resolved, stored)
	}
	continuation = resolved.Tasks[0].Continuation
	if continuation.Version != 5 || continuation.Status != operations.ContinuationResolved || continuation.DecisionBy != operations.DecisionModel || len(continuation.CreatedTaskIDs) != 2 || len(continuation.ProposedActions) != 2 || continuation.ProposedActions[0].Reason != "Check the selected links" || !reflect.DeepEqual(resolved.Tasks[0].Result, parent.Result) {
		t.Fatalf("resolve lost the decision, reason, or original result: %+v", resolved.Tasks[0])
	}
	for i, child := range resolved.Tasks[1:] {
		if !id.Valid(child.ID) || child.ID == parent.ID || child.CommandID != child.ID || child.OperationID != op.ID || child.ParentTaskID != parent.ID || child.StepID != parent.StepID || child.Status != operations.TaskQueued || child.AgentID != "" || continuation.CreatedTaskIDs[i] != child.ID || child.ActionName != continuation.ProposedActions[i].ActionName || !bytes.Equal(child.Input, continuation.ProposedActions[i].Input) {
			t.Fatalf("resolve lost controller-owned child identity or action: %+v", child)
		}
	}
	if resolved.Tasks[1].ID == resolved.Tasks[2].ID {
		t.Fatal("resolve reused a child task ID")
	}
	events := resolved.Events[len(resolved.Events)-3:]
	if events[0].Type != operations.TaskAssigned || events[0].TaskID != resolved.Tasks[1].ID || events[1].Type != operations.TaskAssigned || events[1].TaskID != resolved.Tasks[2].ID || events[2].Type != operations.ContinuationResolvedEvent || events[2].TaskID != parent.ID {
		t.Fatalf("decision and child assignments are missing from persisted events: %+v", events)
	}
	before = operationBytes(t, fixture, op.ID)
	for _, retry := range []string{body, `{"version":3,"actions":[]}`, `{"version":5,"actions":[]}`} {
		assertAPIError(t, server.request(t, http.MethodPost, resolvePath, authenticate, commandBody(retry, "application/json")), http.StatusConflict, "continuation_conflict")
	}
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("decision replay created duplicate children or changed the saved operation")
	}
}

func TestOperationContinuationAcceptsExplicitEmptyActions(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), continuationOperationRecord(1))
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID

	resolved := decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, base+"/tasks/"+op.Tasks[0].ID+"/continuation/resolve", authenticate, commandBody(`{"version":2,"actions":[]}`, "application/json")))
	stored := decodeResponse[operations.Operation](t, server.request(t, http.MethodGet, base, authenticate))

	continuation := resolved.Tasks[0].Continuation
	if !reflect.DeepEqual(resolved, stored) || len(resolved.Tasks) != 1 || continuation.Version != 3 || continuation.Status != operations.ContinuationResolved || continuation.ProposedActions == nil || len(continuation.ProposedActions) != 0 || len(continuation.CreatedTaskIDs) != 0 || resolved.Events[len(resolved.Events)-1].Type != operations.ContinuationResolvedEvent {
		t.Fatalf("explicit empty actions must persist a resolved decision without children: %+v", resolved)
	}
}

func TestOperationContinuationRejectsInvalidRequestsWithoutMutation(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), continuationOperationRecord(1))
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID
	path := base + "/tasks/" + op.Tasks[0].ID + "/continuation/resolve"
	before := operationBytes(t, fixture, op.ID)

	assertAPIError(t, server.request(t, http.MethodPost, path), http.StatusUnauthorized, "authentication_required")
	assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(`{"version":2,"actions":[]}`, "text/plain")), http.StatusUnsupportedMediaType, "unsupported_media_type")
	for _, body := range []string{
		``, `{`, `{} {}`, `null`, `[]`, `{}`, `{"version":2}`, `{"version":2,"actions":null}`, `{"version":2,"actions":{}}`,
		`{"actions":[]}`, `{"version":0,"actions":[]}`, `{"version":-1,"actions":[]}`, `{"version":1.5,"actions":[]}`, `{"version":18446744073709551616,"actions":[]}`,
		`{"version":2,"actions":[null]}`, `{"version":2,"actions":[{}]}`, `{"version":2,"actions":[{"action_name":" "}]}`,
		`{"version":2,"actions":[{"action_name":"review","input":null}]}`, `{"version":2,"actions":[{"action_name":"review","input":[]}]}`,
		`{"version":2,"actions":[{"action_name":"review","input":"text"}]}`, `{"version":2,"actions":[{"action_name":"review","reason":17}]}`,
		`{"version":2,"actions":[{"action_name":"review","agent_id":"review-agent-1234abcd"}]}`,
		`{"version":2,"actions":[],"agent_id":"review-agent-1234abcd"}`,
		fmt.Sprintf(`{"version":2,"actions":[],"operation_id":%q}`, op.ID),
		fmt.Sprintf(`{"version":2,"actions":[],"task_id":%q}`, op.Tasks[0].ID),
	} {
		assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(body, "application/json")), http.StatusBadRequest, "invalid_request")
	}
	for _, invalid := range []string{"not-a-uuid", strings.ToUpper(recordID(0xabcdef)), strings.ReplaceAll(op.ID, "-", "")} {
		assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations/"+invalid+"/tasks/"+op.Tasks[0].ID+"/continuation/resolve", authenticate, commandBody(`{"version":2,"actions":[]}`, "application/json")), http.StatusBadRequest, "invalid_operation_id")
		assertAPIError(t, server.request(t, http.MethodPost, base+"/tasks/"+invalid+"/continuation/resolve", authenticate, commandBody(`{"version":2,"actions":[]}`, "application/json")), http.StatusBadRequest, "invalid_task_id")
	}

	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("invalid continuation requests changed the operation or its revision")
	}
}

func TestOperationContinuationReturnsControllerDomainErrors(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), continuationOperationRecord(1))
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	base := "/api/v1/operations/" + op.ID
	path := base + "/tasks/" + op.Tasks[0].ID + "/continuation/resolve"
	body := `{"version":2,"actions":[]}`
	before := operationBytes(t, fixture, op.ID)

	assertAPIError(t, server.request(t, http.MethodPost, "/api/v1/operations/"+recordID(999)+"/tasks/"+op.Tasks[0].ID+"/continuation/resolve", authenticate, commandBody(body, "application/json")), http.StatusNotFound, "operation_not_found")
	assertAPIError(t, server.request(t, http.MethodPost, base+"/tasks/"+recordID(999)+"/continuation/resolve", authenticate, commandBody(body, "application/json")), http.StatusNotFound, "task_not_found")
	assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(`{"version":1,"actions":[]}`, "application/json")), http.StatusConflict, "continuation_conflict")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("rejected continuation commands changed persisted state")
	}
	decodeResponse[operations.Question](t, server.request(t, http.MethodPost, base+"/questions", authenticate, commandBody(`{"text":"Continue?","step_id":"review-links"}`, "application/json")), http.StatusCreated)
	before = operationBytes(t, fixture, op.ID)
	assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(body, "application/json")), http.StatusConflict, "question_pending")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("continuation resolution bypassed the pending step question")
	}
	decodeResponse[operations.Operation](t, server.request(t, http.MethodPost, base+"/cancel", authenticate))
	before = operationBytes(t, fixture, op.ID)
	assertAPIError(t, server.request(t, http.MethodPost, path, authenticate, commandBody(body, "application/json")), http.StatusConflict, "operation_finished")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("continuation resolution changed a finished operation")
	}
}

func TestOperationContinuationEnforcesTaskLimitWithoutPartialChildren(t *testing.T) {
	fixture := newFixture(t)
	op := saveOperation(t, operationStore(t, fixture), continuationOperationRecord(1))
	startCommandController(t, fixture, nil)
	server := startAPI(t, fixture.localArgs())
	before := operationBytes(t, fixture, op.ID)
	body := `{"version":2,"actions":[` + strings.Repeat(`{"action_name":"review"},`, 9999) + `{"action_name":"review"}]}`

	result := server.request(t, http.MethodPost, "/api/v1/operations/"+op.ID+"/tasks/"+op.Tasks[0].ID+"/continuation/resolve", authenticate, commandBody(body, "application/json"))

	assertAPIError(t, result, http.StatusConflict, "task_limit_exceeded")
	if !bytes.Equal(before, operationBytes(t, fixture, op.ID)) {
		t.Fatal("task limit rejection partially created children or changed the continuation")
	}
}

func continuationOperationRecord(number int) operations.Operation {
	op := operationRecord(number)
	op.Mission.Steps = []missions.Step{{ID: "review-links", Objective: "Review links", RequiredActions: []string{"review"}}}
	op.Tasks = []operations.Task{taskRecord(op.ID, 20)}
	op.Tasks[0].ParentTaskID = ""
	op.Tasks[0].Continuation.CreatedTaskIDs = []string{}
	op.Preflight = operations.AssessPreflight(op.Mission, nil)
	op.RefreshStatus()
	return op
}

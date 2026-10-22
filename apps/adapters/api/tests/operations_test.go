package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

func TestOperationExecutesProposedWorkAndKeepsResults(t *testing.T) {
	a := connect(t)
	initialAgents := read[models.ListResponse[models.AgentResponse]](a, "/api/v1/agents")
	initialActions := read[models.ListResponse[models.ActionResponse]](a, "/api/v1/actions")
	created := a.request(http.MethodPost, "/api/v1/operations", object{"mission_name": "analyze-text", "input": object{"text": fixtureText}})
	op := decode[models.OperationResponse](t, created, http.StatusCreated)
	base := operationPath(op.ID)
	if created.header.Get("Location") != base || op.Status != "planning" || op.Preflight == nil || !op.Preflight.Ready || len(op.Tasks) != 0 {
		t.Fatalf("new operation lacks identity, readiness or empty tasks: %+v headers=%v", op, created.header)
	}
	assertSchema(t, a, "Operation", created)
	if op.Progress.TotalTasks != 0 || op.Progress.FinishedTasks != 0 || op.Progress.Settled || op.Progress.TaskCounts == nil {
		t.Fatalf("new operation progress: %+v", op.Progress)
	}
	assertError(t, a.request(http.MethodPost, base+"/tasks", object{"action_name": "analyze-text", "step_id": "unknown", "input": object{"text": fixtureText}}), http.StatusUnprocessableEntity, "invalid_input")
	assigned := a.request(http.MethodPost, base+"/tasks", object{"action_name": "analyze-text", "input": object{"text": fixtureText}})
	root := decode[models.TaskResponse](t, assigned, http.StatusCreated)
	if assigned.header.Get("Location") != base+"/tasks/"+root.ID || root.OperationID != op.ID || root.CommandID == "" {
		t.Fatalf("assignment lost task identity: %+v", root)
	}
	settled := a.wait(op.ID, func(op models.OperationResponse) bool { return tasksSettled(op) && modelDecision(op) != nil })
	if len(settled.Tasks) != 2 || settled.Tasks[0].ActionName != "analyze-text" || settled.Tasks[1].ActionName != "write-summary" {
		t.Fatalf("controller did not execute the proposed summary action: %+v", settled.Tasks)
	}
	for _, task := range settled.Tasks {
		if task.Status != "succeeded" || task.Result == nil || task.OperationID != op.ID || task.AgentID == "" {
			t.Fatalf("unsuccessful or unowned task: %+v", task)
		}
		coverage := slices.IndexFunc(op.Preflight.Actions, func(item models.ActionReadinessResponse) bool { return item.ActionName == task.ActionName })
		if coverage < 0 || !slices.Contains(op.Preflight.Actions[coverage].ReadyAgentIDs, task.AgentID) {
			t.Fatalf("controller selected an agent without the task's capability: %+v", task)
		}
		if task.ID != root.ID && task.ParentTaskID != root.ID {
			t.Fatalf("automatic task lost its proposal parent: %+v", task)
		}
		if task.ID != root.ID && (task.Continuation == nil || task.Continuation.Status != "resolved") {
			t.Fatalf("group member exposed a separate decision: %+v", task)
		}
	}
	assertTaskAndEventPages(t, a, settled)
	decision := modelDecision(settled)
	if decision.ID != root.ID || len(decision.Continuation.CreatedTaskIDs) != 1 || decision.Continuation.CreatedTaskIDs[0] != settled.Tasks[1].ID {
		t.Fatalf("grouped decision belongs to %s, want root %s", decision.ID, root.ID)
	}
	a.resolve(op.ID, *decision, []object{})
	assertError(t, a.request(http.MethodPost, base+"/complete", object{"output": object{}}), http.StatusUnprocessableEntity, "invalid_output")
	completed := a.complete(op.ID, "Text analysis completed")
	if completed.Status != "completed" || completed.ReportID == "" || !completed.Progress.Settled || completed.Progress.FinishedTasks != len(completed.Tasks) || completed.Progress.TotalTasks != len(completed.Tasks) {
		t.Fatalf("completion: %+v", completed)
	}
	byID := read[models.ReportResponse](a, "/api/v1/reports/"+completed.ReportID)
	byOperation := read[models.ReportResponse](a, base+"/report")
	if !reflect.DeepEqual(byID, byOperation) || byID.OperationID != op.ID || byID.Mission != "analyze-text" || !strings.Contains(string(byID.Output), "Text analysis completed") {
		t.Fatalf("report identity/output changed across routes: %+v %+v", byID, byOperation)
	}
	assertSchema(t, a, "Report", a.request(http.MethodGet, base+"/report", nil))
	assertError(t, a.request(http.MethodPost, base+"/tasks", object{"action_name": "analyze-text", "input": object{"text": fixtureText}}), http.StatusConflict, "operation_finished")
	before := a.request(http.MethodGet, base, nil)
	for _, path := range []string{base, base + "/report", "/api/v1/reports/" + completed.ReportID, base + "/tasks", base + "/events", base + "/graph"} {
		if got := a.request(http.MethodGet, path, nil); got.status != http.StatusOK {
			t.Fatalf("read %s: %+v", path, got)
		}
	}
	if after := a.request(http.MethodGet, base, nil); after.body != before.body {
		t.Fatal("completed operation queries changed saved state")
	}
	if len(completed.Tasks[1].Result.Artifacts) != 1 || len(completed.Tasks[1].Result.Artifacts[0].Files) != 1 {
		t.Fatal("summary task did not publish downloadable evidence")
	}
	artifact := completed.Tasks[1].Result.Artifacts[0]
	artifactPath := "/api/v1/artifacts/" + artifact.ID
	filePath := artifactPath + "/files/" + artifact.Files[0].ID
	for _, path := range []string{artifactPath, filePath} {
		if result := a.request(http.MethodGet, path, nil); result.status != http.StatusOK {
			t.Fatalf("evidence was unavailable before cleanup: %s: %+v", path, result)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.settle(ctx); err != nil {
		t.Fatal(err)
	}
	cleaned, err := a.send(ctx, http.MethodPost, "/api/v1/testing/cleanup", nil)
	if err != nil || cleaned.status != http.StatusNoContent || cleaned.body != "" {
		t.Fatalf("cleanup of completed workflow: %+v: %v", cleaned, err)
	}
	removed := []struct{ path, code string }{
		{base, "operation_not_found"}, {base + "/report", "operation_not_found"},
		{"/api/v1/reports/" + completed.ReportID, "report_not_found"},
		{artifactPath, "artifact_not_found"}, {filePath, "artifact_not_found"},
	}
	for _, tc := range removed {
		assertError(t, a.request(http.MethodGet, tc.path, nil), http.StatusNotFound, tc.code)
	}
	if missions := read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions"); !slices.Equal(missionNames(missions.Items), expectedMissions) {
		t.Fatalf("cleanup changed the mission catalog: %+v", missions)
	}
	if actions := read[models.ListResponse[models.ActionResponse]](a, "/api/v1/actions"); !reflect.DeepEqual(actions, initialActions) {
		t.Fatalf("cleanup changed the action catalog: %+v", actions)
	}
	if agents := read[models.ListResponse[models.AgentResponse]](a, "/api/v1/agents"); !slices.EqualFunc(agents.Items, initialAgents.Items, func(left, right models.AgentResponse) bool { return left.ID == right.ID }) {
		t.Fatalf("cleanup changed the agent catalog: %+v", agents)
	}
}

func TestOperationStartReplayCancelAndFail(t *testing.T) {
	a := connect(t)
	for i, command := range []string{"cancel", "fail"} {
		t.Run(command, func(t *testing.T) {
			id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
			body := object{"operation_id": id, "mission_name": "analyze-text", "input": object{"text": fixtureText}}
			op := post[models.OperationResponse](a, "/api/v1/operations", body, http.StatusCreated)
			if op.ID != id {
				t.Fatalf("explicit operation ID changed: %s", op.ID)
			}
			assertError(t, a.request(http.MethodPost, "/api/v1/operations", body), http.StatusConflict, "operation_conflict")
			task := a.task(op.ID, "unavailable-test-action", object{})
			if task.Status != "queued" || task.AgentID != "" {
				t.Fatalf("unavailable action should remain queued: %+v", task)
			}
			var commandBody any
			wantStatus := "cancelled"
			if command == "fail" {
				commandBody, wantStatus = object{"reason": "Operator ended this test"}, "failed"
			}
			ended := post[models.OperationResponse](a, operationPath(op.ID)+"/"+command, commandBody, http.StatusOK)
			if ended.Status != wantStatus || len(ended.Tasks) != 1 || ended.Tasks[0].Status != "cancelled" || !ended.Progress.Settled || ended.Progress.FinishedTasks != 1 || ended.Progress.TotalTasks != 1 {
				t.Fatalf("terminal operation: %+v", ended)
			}
			if command == "fail" && ended.Error != "Operator ended this test" {
				t.Fatalf("failure reason changed: %+v", ended)
			}
			assertError(t, a.request(http.MethodGet, operationPath(op.ID)+"/report", nil), http.StatusNotFound, "report_not_found")
			assertError(t, a.request(http.MethodPost, operationPath(op.ID)+"/cancel", nil), http.StatusConflict, "operation_finished")
		})
	}
	list := read[models.ListResponse[models.OperationResponse]](a, "/api/v1/operations")
	if len(list.Items) != 2 || list.Items[0].CreatedAt.Before(list.Items[1].CreatedAt) {
		t.Fatalf("operation list order: %+v", list)
	}
}

func TestOperationCommandsRejectInvalidRequests(t *testing.T) {
	a := connect(t)
	op := a.start(fixtureText)
	base := operationPath(op.ID)
	for _, body := range []any{object{}, object{"mission_name": "analyze-text"}, object{"mission_name": "analyze-text", "input": object{}, "unknown": true}} {
		assertError(t, a.request(http.MethodPost, "/api/v1/operations", body), http.StatusBadRequest, "invalid_request")
	}
	assertError(t, a.request(http.MethodPost, "/api/v1/operations", object{"mission_name": "missing", "input": object{}}), http.StatusNotFound, "mission_not_found")
	for _, input := range []any{nil, object{}, []string{"text"}, "unexpected text value", true} {
		assertError(t, a.request(http.MethodPost, "/api/v1/operations", object{"mission_name": "analyze-text", "input": input}), http.StatusUnprocessableEntity, "invalid_input")
	}
	for _, body := range []object{{"action_name": "analyze-text", "agent_id": "pinned"}, {"action_name": "analyze-text", "operation_id": op.ID}, {"action_name": "analyze-text", "input": nil}} {
		assertError(t, a.request(http.MethodPost, base+"/tasks", body), http.StatusBadRequest, "invalid_request")
	}
	for _, tc := range []struct {
		content, media string
		status         int
		code           string
	}{
		{"{", "application/json", 400, "invalid_request"},
		{"{} {}", "application/json", 400, "invalid_request"},
		{"{}", "text/plain", 415, "unsupported_media_type"},
		{strings.Repeat("x", (1<<20)+1), "application/json", 413, "request_too_large"},
	} {
		result := a.request(http.MethodPost, base+"/tasks", nil, func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(tc.content))
			r.ContentLength = int64(len(tc.content))
			r.Header.Set("Content-Type", tc.media)
		})
		assertError(t, result, tc.status, tc.code)
	}
	if got := read[models.OperationResponse](a, base); len(got.Tasks) != 0 || len(got.Questions) != 0 {
		t.Fatal("invalid requests created work")
	}
}

func assertTaskAndEventPages(t *testing.T, a *api, op models.OperationResponse) {
	t.Helper()
	base := operationPath(op.ID)
	var tasks []models.TaskResponse
	cursor := ""
	for {
		page := read[models.PageResponse[models.TaskResponse]](a, base+"/tasks?limit=1&cursor="+cursor)
		if page.Items == nil {
			t.Fatal("task page items must be an array")
		}
		if len(page.Items) == 0 {
			if page.NextCursor != cursor {
				t.Fatal("empty task page changed cursor")
			}
			break
		}
		if page.NextCursor != page.Items[len(page.Items)-1].ID {
			t.Fatal("task cursor does not identify last item")
		}
		tasks, cursor = append(tasks, page.Items...), page.NextCursor
	}
	if !reflect.DeepEqual(tasks, op.Tasks) {
		t.Fatal("task pagination changed assignment order or records")
	}
	for _, task := range tasks {
		if detail := read[models.TaskResponse](a, base+"/tasks/"+task.ID); !reflect.DeepEqual(detail, task) {
			t.Fatalf("task detail differs: %+v", detail)
		}
	}
	var events []models.EventResponse
	cursor = ""
	for {
		page := read[models.PageResponse[models.EventResponse]](a, base+"/events?limit=3&cursor="+cursor)
		if page.Items == nil {
			t.Fatal("event page items must be an array")
		}
		if len(page.Items) == 0 {
			if page.NextCursor != cursor {
				t.Fatal("empty event page changed cursor")
			}
			break
		}
		if page.NextCursor != fmt.Sprint(page.Items[len(page.Items)-1].Sequence) {
			t.Fatal("event cursor does not identify last sequence")
		}
		events, cursor = append(events, page.Items...), page.NextCursor
	}
	if !reflect.DeepEqual(events, op.Events) {
		t.Fatal("event pagination lost durable events")
	}
}

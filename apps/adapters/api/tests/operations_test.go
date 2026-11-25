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
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/testing/missiontest"
)

func TestOperationExecutesProposedWorkAndKeepsResults(t *testing.T) {
	a := fixtures.Connect(t)
	driver := a.MissionDriver()
	driver.WaitForActions("rank-hn-stories", "write-hn-digest")
	initialAgents := fixtures.Read[models.ListResponse[models.AgentResponse]](a, "/api/v1/agents")
	initialActions := fixtures.Read[models.ListResponse[models.ActionResponse]](a, "/api/v1/actions")
	input := fixtures.StoryBatch(fixtures.FixtureTitle)
	started := driver.Start("hacker-news-test", input)
	base := fixtures.OperationPath(started.ID)
	created := a.Request(http.MethodGet, base, nil)
	op := fixtures.Decode[models.OperationResponse](t, created, http.StatusOK)
	if started.Status != operations.Planning || started.Preflight == nil || !started.Preflight.Ready || len(started.Tasks) != 0 {
		t.Fatalf("creation response lacks readiness or empty tasks: %+v", started)
	}
	if op.Status != "planning" || op.Preflight == nil || !op.Preflight.Ready || len(op.Tasks) != 0 {
		t.Fatalf("new operation lacks readiness or empty tasks: %+v", op)
	}
	assertSchema(t, a, "Operation", created)
	if op.Progress.TotalTasks != 0 || op.Progress.FinishedTasks != 0 || op.Progress.Settled || op.Progress.TaskCounts == nil {
		t.Fatalf("new operation progress: %+v", op.Progress)
	}
	root := driver.Assign(op.ID, missiontest.Assignment{ActionName: "rank-hn-stories", Input: input})
	if root.OperationID != op.ID || root.CommandID == "" {
		t.Fatalf("assignment lost task identity: %+v", root)
	}
	review := driver.WaitForContinuation(op.ID, root.ID)
	settled := fixtures.Read[models.OperationResponse](a, base)
	if len(settled.Tasks) != 2 || settled.Tasks[0].ActionName != "rank-hn-stories" || settled.Tasks[1].ActionName != "write-hn-digest" {
		t.Fatalf("controller did not execute the proposed digest action: %+v", settled.Tasks)
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
	decision := fixtures.ModelDecision(settled)
	if decision == nil || decision.ID != root.ID || decision.ID != review.ID || decision.Continuation.Version != review.Continuation.Version ||
		len(decision.Continuation.CreatedTaskIDs) != 1 || decision.Continuation.CreatedTaskIDs[0] != settled.Tasks[1].ID {
		t.Fatalf("grouped decision differs from root review %s: %+v", root.ID, decision)
	}
	driver.Resolve(op.ID, review)
	fixtures.AssertError(t, a.Request(http.MethodPost, base+"/complete", fixtures.Object{"output": fixtures.Object{}}), http.StatusUnprocessableEntity, "invalid_output")
	result := driver.Complete(op.ID, fixtures.Object{"summary": "Story digest completed"})
	completed := fixtures.Read[models.OperationResponse](a, base)
	if result.Status != operations.Completed || result.CompletedBy != operations.CompletionOperator || result.ReportID != completed.ReportID ||
		completed.Status != "completed" || completed.ReportID == "" || !completed.Progress.Settled || completed.Progress.FinishedTasks != len(completed.Tasks) || completed.Progress.TotalTasks != len(completed.Tasks) {
		t.Fatalf("completion: %+v", completed)
	}
	byID := fixtures.Read[models.ReportResponse](a, "/api/v1/reports/"+completed.ReportID)
	byOperation := driver.Report(op.ID)
	if byID.ID != byOperation.ID || byID.OperationID != byOperation.OperationID || byID.Mission != byOperation.Mission ||
		!byID.CreatedAt.Equal(byOperation.CreatedAt) || !reflect.DeepEqual(byID.Output, byOperation.Output) ||
		byID.OperationID != op.ID || byID.Mission != "hacker-news-test" || !strings.Contains(string(byID.Output), "Story digest completed") {
		t.Fatalf("report identity/output changed across routes: %+v %+v", byID, byOperation)
	}
	assertSchema(t, a, "Report", a.Request(http.MethodGet, base+"/report", nil))
	fixtures.AssertError(t, a.Request(http.MethodPost, base+"/tasks", fixtures.Object{"action_name": "rank-hn-stories", "input": fixtures.StoryBatch(fixtures.FixtureTitle)}), http.StatusConflict, "operation_finished")
	before := a.Request(http.MethodGet, base, nil)
	for _, path := range []string{base, base + "/report", "/api/v1/reports/" + completed.ReportID, base + "/tasks", base + "/events", base + "/graph"} {
		if got := a.Request(http.MethodGet, path, nil); got.Status != http.StatusOK {
			t.Fatalf("read %s: %+v", path, got)
		}
	}
	if after := a.Request(http.MethodGet, base, nil); after.Body != before.Body {
		t.Fatal("completed operation queries changed saved state")
	}
	if len(completed.Tasks[1].Result.Artifacts) != 1 || len(completed.Tasks[1].Result.Artifacts[0].Files) != 2 {
		t.Fatal("digest task did not publish downloadable evidence")
	}
	artifact := completed.Tasks[1].Result.Artifacts[0]
	artifactPath := "/api/v1/artifacts/" + artifact.ID
	filePath := artifactPath + "/files/" + artifact.Files[0].ID
	if result := a.Request(http.MethodGet, artifactPath, nil); result.Status != http.StatusOK {
		t.Fatalf("evidence was unavailable before cleanup: %+v", result)
	}
	file := artifact.Files[0]
	data := driver.Download(artifact.ID, artifacts.ArtifactFile{
		ID: file.ID, Name: file.Name, MediaType: file.MediaType, Size: file.Size, SHA256: file.SHA256,
	})
	if !strings.Contains(string(data), fixtures.FixtureTitle) {
		t.Fatal("verified artifact lost the supplied story")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.Settle(ctx); err != nil {
		t.Fatal(err)
	}
	cleaned, err := a.Send(ctx, http.MethodPost, "/api/v1/testing/cleanup", nil)
	if err != nil || cleaned.Status != http.StatusNoContent || cleaned.Body != "" {
		t.Fatalf("cleanup of completed workflow: %+v: %v", cleaned, err)
	}
	removed := []struct{ path, code string }{
		{base, "operation_not_found"}, {base + "/report", "operation_not_found"},
		{"/api/v1/reports/" + completed.ReportID, "report_not_found"},
		{artifactPath, "artifact_not_found"}, {filePath, "artifact_not_found"},
	}
	for _, tc := range removed {
		fixtures.AssertError(t, a.Request(http.MethodGet, tc.path, nil), http.StatusNotFound, tc.code)
	}
	if missions := fixtures.Read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions"); !fixtures.HasExpectedMissions(missions.Items) {
		t.Fatalf("cleanup changed the mission catalog: %+v", missions)
	}
	if actions := fixtures.Read[models.ListResponse[models.ActionResponse]](a, "/api/v1/actions"); !reflect.DeepEqual(actions, initialActions) {
		t.Fatalf("cleanup changed the action catalog: %+v", actions)
	}
	if agents := fixtures.Read[models.ListResponse[models.AgentResponse]](a, "/api/v1/agents"); !slices.EqualFunc(agents.Items, initialAgents.Items, func(left, right models.AgentResponse) bool { return left.ID == right.ID }) {
		t.Fatalf("cleanup changed the agent catalog: %+v", agents)
	}
}

func TestOperationStartReplayCancelAndFail(t *testing.T) {
	a := fixtures.Connect(t)
	for i, command := range []string{"cancel", "fail"} {
		t.Run(command, func(t *testing.T) {
			id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
			body := fixtures.Object{"operation_id": id, "mission_name": "hacker-news-test", "input": fixtures.StoryBatch(fixtures.FixtureTitle)}
			created := a.Request(http.MethodPost, "/api/v1/operations", body)
			op := fixtures.Decode[models.OperationResponse](t, created, http.StatusCreated)
			if op.ID != id || created.Header.Get("Location") != fixtures.OperationPath(id) {
				t.Fatalf("explicit operation identity or creation location changed: %s", op.ID)
			}
			assertSchema(t, a, "Operation", created)
			if op.Progress.TotalTasks != 0 || op.Progress.FinishedTasks != 0 || op.Progress.Settled || op.Progress.TaskCounts == nil {
				t.Fatalf("creation response has incorrect progress: %+v", op.Progress)
			}
			fixtures.AssertError(t, a.Request(http.MethodPost, "/api/v1/operations", body), http.StatusConflict, "operation_conflict")
			task := a.Task(op.ID, "unavailable-test-action", fixtures.Object{})
			if task.Status != "queued" || task.AgentID != "" {
				t.Fatalf("unavailable action should remain queued: %+v", task)
			}
			var commandBody any
			wantStatus := "cancelled"
			if command == "fail" {
				commandBody, wantStatus = fixtures.Object{"reason": "Operator ended this test"}, "failed"
			}
			ended := fixtures.Post[models.OperationResponse](a, fixtures.OperationPath(op.ID)+"/"+command, commandBody, http.StatusOK)
			if ended.Status != wantStatus || len(ended.Tasks) != 1 || ended.Tasks[0].Status != "cancelled" || !ended.Progress.Settled || ended.Progress.FinishedTasks != 1 || ended.Progress.TotalTasks != 1 {
				t.Fatalf("terminal operation: %+v", ended)
			}
			if command == "fail" && ended.Error != "Operator ended this test" {
				t.Fatalf("failure reason changed: %+v", ended)
			}
			fixtures.AssertError(t, a.Request(http.MethodGet, fixtures.OperationPath(op.ID)+"/report", nil), http.StatusNotFound, "report_not_found")
			fixtures.AssertError(t, a.Request(http.MethodPost, fixtures.OperationPath(op.ID)+"/cancel", nil), http.StatusConflict, "operation_finished")
		})
	}
	list := fixtures.Read[models.ListResponse[models.OperationResponse]](a, "/api/v1/operations")
	if len(list.Items) != 2 || list.Items[0].CreatedAt.Before(list.Items[1].CreatedAt) {
		t.Fatalf("operation list order: %+v", list)
	}
}

func TestOperationCommandsRejectInvalidRequests(t *testing.T) {
	a := fixtures.Connect(t)
	op := a.Start(fixtures.FixtureTitle)
	base := fixtures.OperationPath(op.ID)
	for _, body := range []any{fixtures.Object{}, fixtures.Object{"mission_name": "hacker-news-test"}, fixtures.Object{"mission_name": "hacker-news-test", "input": fixtures.Object{}, "unknown": true}} {
		fixtures.AssertError(t, a.Request(http.MethodPost, "/api/v1/operations", body), http.StatusBadRequest, "invalid_request")
	}
	fixtures.AssertError(t, a.Request(http.MethodPost, "/api/v1/operations", fixtures.Object{"mission_name": "missing", "input": fixtures.Object{}}), http.StatusNotFound, "mission_not_found")
	for _, input := range []any{nil, fixtures.Object{}, []string{"text"}, "unexpected text value", true} {
		fixtures.AssertError(t, a.Request(http.MethodPost, "/api/v1/operations", fixtures.Object{"mission_name": "hacker-news-test", "input": input}), http.StatusUnprocessableEntity, "invalid_input")
	}
	for _, body := range []fixtures.Object{{"action_name": "rank-hn-stories", "agent_id": "pinned"}, {"action_name": "rank-hn-stories", "operation_id": op.ID}, {"action_name": "rank-hn-stories", "input": nil}} {
		fixtures.AssertError(t, a.Request(http.MethodPost, base+"/tasks", body), http.StatusBadRequest, "invalid_request")
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
		result := a.Request(http.MethodPost, base+"/tasks", nil, func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(tc.content))
			r.ContentLength = int64(len(tc.content))
			r.Header.Set("Content-Type", tc.media)
		})
		fixtures.AssertError(t, result, tc.status, tc.code)
	}
	if got := fixtures.Read[models.OperationResponse](a, base); len(got.Tasks) != 0 || len(got.Questions) != 0 {
		t.Fatal("invalid requests created work")
	}
}

func assertTaskAndEventPages(t *testing.T, a *fixtures.Client, op models.OperationResponse) {
	t.Helper()
	base := fixtures.OperationPath(op.ID)
	var tasks []models.TaskResponse
	cursor := ""
	for {
		page := fixtures.Read[models.PageResponse[models.TaskResponse]](a, base+"/tasks?limit=1&cursor="+cursor)
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
		if detail := fixtures.Read[models.TaskResponse](a, base+"/tasks/"+task.ID); !reflect.DeepEqual(detail, task) {
			t.Fatalf("task detail differs: %+v", detail)
		}
	}
	var events []models.EventResponse
	cursor = ""
	for {
		page := fixtures.Read[models.PageResponse[models.EventResponse]](a, base+"/events?limit=3&cursor="+cursor)
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

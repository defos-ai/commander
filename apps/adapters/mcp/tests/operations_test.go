package mcp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConcurrentOperationsStayIsolated(t *testing.T) {
	// Hold both requests so capacity and isolation are observable without timing races.
	a := connect(t)
	b := openSession(t)
	url, entered, release := heldWebsite(t, "/first", "/second")
	first, second := a.start(url+"/first"), b.start(url+"/second")
	var empty struct {
		Tasks      []operations.Task `json:"tasks"`
		NextCursor *string           `json:"next_cursor"`
	}
	a.call("operation_tasks", object{"operation_id": first.ID}, &empty)
	if empty.Tasks == nil || len(empty.Tasks) != 0 || empty.NextCursor == nil || *empty.NextCursor != "" {
		t.Fatalf("new operation must have an empty task list: %+v", empty)
	}
	a.task(first.ID, "check-seo", url+"/first")
	b.task(second.ID, "check-seo", url+"/second")
	for range 2 {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("both operations should make progress concurrently")
		}
	}
	left, right := a.get(first.ID), b.get(second.ID)
	if left.Tasks[0].Status != operations.TaskRunning || right.Tasks[0].Status != operations.TaskRunning || left.Tasks[0].AgentID == right.Tasks[0].AgentID {
		t.Fatalf("capacity was not respected: first=%+v second=%+v", left.Tasks, right.Tasks)
	}
	assertWork(t, a, left.Tasks[0], right.Tasks[0])
	queued := a.task(first.ID, "check-seo", url+"/extra")
	if queued.Status != operations.TaskQueued {
		t.Fatalf("third HTTP task exceeded the two web agents' capacity: %+v", queued)
	}
	var question operations.Question
	a.call("ask_question", object{"operation_id": first.ID, "text": "Approve the first review?", "options": []string{"Yes", "No"}}, &question)
	assertTaskQueries(t, a, a.get(first.ID))
	assertTaskQueries(t, b, right)
	a.fails("get_task", object{"operation_id": first.ID, "task_id": right.Tasks[0].ID}, "unknown task")
	a.fails("get_task", object{"operation_id": first.ID, "task_id": id.New()}, "unknown task")
	a.fails("get_task", object{"operation_id": first.ID, "task_id": "not-a-uuid"}, "UUID")
	a.fails("get_task", object{"operation_id": id.OperationID(), "task_id": left.Tasks[0].ID}, "unknown operation")
	a.fails("operation_tasks", object{"operation_id": id.OperationID()}, "unknown operation")
	a.fails("operation_tasks", object{"operation_id": first.ID, "cursor": right.Tasks[0].ID}, "unknown task")
	a.fails("operation_tasks", object{"operation_id": first.ID, "cursor": "invalid"}, "UUID")
	assertInvalidPageLimits(t, "operation_tasks", first.ID)

	release("/second")
	b.wait(second.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	b.task(second.ID, "capture-page", url+"/second")
	right = b.wait(second.ID, allTasksSucceeded)
	if len(right.Questions) != 0 || a.get(first.ID).Tasks[1].Status != operations.TaskQueued {
		t.Fatal("one operation's question affected the other operation or lost its dispatch barrier")
	}
	release("/first")
	a.wait(first.ID, func(op operationDetail) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
	a.call("answer_question", object{"operation_id": first.ID, "question_id": question.ID, "answer": "Yes"}, nil)
	a.wait(first.ID, allTasksSucceeded)
	a.task(first.ID, "capture-page", url+"/first")
	left = a.wait(first.ID, allTasksSucceeded)

	artifactOwners := map[string]string{}
	var previousArtifact *artifacts.Artifact
	for _, op := range []operationDetail{left, right} {
		assertTaskQueries(t, a, op)
		for _, task := range op.Tasks {
			if task.OperationID != op.ID || task.Result == nil {
				t.Fatalf("task crossed operation boundaries: %+v", task)
			}
			var input, output struct{ URL string }
			if err := json.Unmarshal(task.Input, &input); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(task.Result.Output, &output); err != nil || output.URL != input.URL {
				t.Fatalf("result does not match task input: %s: %s (%v)", task.ID, task.Result.Output, err)
			}
			if task.ActionName == "capture-page" && len(task.Result.Artifacts) != 1 {
				t.Fatalf("missing screenshots: %+v", task.Result)
			}
			for _, ref := range task.Result.Artifacts {
				if ref.CommandID != task.CommandID || ref.AgentID != task.AgentID || artifactOwners[ref.ID] != "" {
					t.Fatalf("incorrect artifact ownership: %+v", ref)
				}
				artifactOwners[ref.ID] = op.ID
				assertScreenshots(t, a, ref)
				if previousArtifact != nil {
					uri := "missions://artifacts/" + ref.ID + "/files/" + previousArtifact.Files[0].ID
					invalid := openSession(t)
					if _, err := invalid.session.ReadResource(invalid.ctx, &mcp.ReadResourceParams{URI: uri}); err == nil || !strings.Contains(err.Error(), "Resource not found") {
						t.Fatalf("expected another artifact's file to be inaccessible, got %v", err)
					}
				}
				previousArtifact = &ref
			}
		}
		summary := "Review " + op.ID
		a.call("complete_operation", object{"operation_id": op.ID, "output": object{"url": url, "summary": summary, "findings": []object{}}}, nil)
		var report reports.Report
		b.call("get_report", object{"operation_id": op.ID}, &report)
		if report.OperationID != op.ID || !bytes.Contains(report.Output, []byte(summary)) {
			t.Fatalf("report crossed operation boundaries: %+v", report)
		}
	}
	if len(left.Questions) != 1 || left.Questions[0].Answer != "Yes" || len(right.Questions) != 0 || len(artifactOwners) != 2 {
		t.Fatal("questions or artifacts leaked between operations")
	}
}

func assertWork(t *testing.T, a *api, tasks ...operations.Task) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var list struct {
			Agents []agents.Agent `json:"agents"`
		}
		a.call("list_agents", object{}, &list)
		matched := 0
		for _, task := range tasks {
			for _, agent := range list.Agents {
				if agent.ID != task.AgentID {
					continue
				}
				work := agent.Work
				if agent.Stale || agent.Status != agents.AgentWorking || work == nil || work.OperationID != task.OperationID || work.TaskID != task.ID || work.CommandID != task.CommandID || work.ActionName != task.ActionName {
					continue
				}
				var detail agents.Agent
				a.call("describe_agent", object{"agent_id": agent.ID}, &detail)
				if !reflect.DeepEqual(detail.Work, work) {
					t.Fatalf("agent queries disagree about current work: list=%+v detail=%+v", work, detail.Work)
				}
				matched++
			}
		}
		if matched == len(tasks) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent queries did not report running tasks: %+v", list.Agents)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func assertTaskQueries(t *testing.T, a *api, op operationDetail) {
	t.Helper()
	var list struct {
		Tasks      []json.RawMessage `json:"tasks"`
		NextCursor string            `json:"next_cursor"`
	}
	a.call("operation_tasks", object{"operation_id": op.ID}, &list)
	if len(list.Tasks) != len(op.Tasks) || list.NextCursor == "" {
		t.Fatalf("task count differs from operation: got %d, want %d", len(list.Tasks), len(op.Tasks))
	}
	var cursor string
	for i := 0; i <= len(list.Tasks); i++ {
		var page struct {
			Tasks      []json.RawMessage `json:"tasks"`
			NextCursor string            `json:"next_cursor"`
		}
		a.call("operation_tasks", object{"operation_id": op.ID, "cursor": cursor, "limit": 1}, &page)
		if i == len(list.Tasks) {
			if len(page.Tasks) != 0 || page.NextCursor != cursor || cursor != list.NextCursor {
				t.Fatalf("empty task page did not preserve final cursor %q: %+v", cursor, page)
			}
			break
		}
		if len(page.Tasks) != 1 || !bytes.Equal(page.Tasks[0], list.Tasks[i]) || page.NextCursor == "" || page.NextCursor == cursor {
			t.Fatalf("task pagination lost assignment order at index %d: %+v", i, page)
		}
		cursor = page.NextCursor
	}
	for i, raw := range list.Tasks {
		var summary operations.Task
		if err := json.Unmarshal(raw, &summary); err != nil {
			t.Fatal(err)
		}
		want := op.Tasks[i]
		if summary.ID != want.ID || summary.ParentTaskID != want.ParentTaskID || summary.StepID != want.StepID || summary.AgentID != want.AgentID ||
			summary.ActionName != want.ActionName || summary.Status != want.Status || summary.Error != want.Error ||
			!summary.StartedAt.Equal(want.StartedAt) || !reflect.DeepEqual(summary.FinishedAt, want.FinishedAt) {
			t.Fatalf("task summary lost order or progress: got %+v, want %+v", summary, want)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"input", "result", "command_id", "deadline_at"} {
			if _, exists := fields[name]; exists {
				t.Errorf("task summary repeats detail field %s", name)
			}
		}
		if want.Continuation == nil {
			if summary.Continuation != nil {
				t.Fatalf("task summary invented continuation: %+v", summary)
			}
		} else {
			c := summary.Continuation
			if c == nil || c.Version != want.Continuation.Version || c.Status != want.Continuation.Status || c.DecisionBy != want.Continuation.DecisionBy {
				t.Fatalf("task summary lost continuation decision: got %+v, want %+v", c, want.Continuation)
			}
			if bytes.Contains(fields["continuation"], []byte(`"proposed_actions"`)) || bytes.Contains(fields["continuation"], []byte(`"created_task_ids"`)) {
				t.Fatal("task summary repeats continuation payloads")
			}
		}
		var detail operations.Task
		a.call("get_task", object{"operation_id": op.ID, "task_id": want.ID}, &detail)
		if !reflect.DeepEqual(detail, want) {
			t.Fatalf("task detail differs from operation snapshot: got %+v, want %+v", detail, want)
		}
	}
}

func TestCancellationWhileActionIsRunning(t *testing.T) {
	a := connect(t)
	url, entered, release := heldWebsite(t, "/slow", "/blocker")
	op := a.start(url + "/slow")
	a.call("assign_task", object{"operation_id": op.ID, "action_name": "discover-pages", "input": object{"url": url + "/slow", "max_pages": 1}}, nil)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("discovery did not start")
	}
	blocker := a.start(url + "/blocker")
	a.task(blocker.ID, "check-seo", url+"/blocker")
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("second agent did not start the blocking task")
	}
	queued := a.task(op.ID, "check-seo", url+"/queued")
	if queued.Status != operations.TaskQueued || queued.AgentID != "" {
		t.Fatalf("task should await capacity without an assigned agent: %+v", queued)
	}

	a.call("cancel_operation", object{"operation_id": op.ID}, nil)
	cancelled := a.get(op.ID)
	if cancelled.Status != operations.Cancelled || cancelled.Tasks[1].Status != operations.TaskCancelled {
		t.Fatalf("queued work was not cancelled: %+v", cancelled)
	}
	release("/slow")
	settled := a.wait(op.ID, func(op operationDetail) bool { return op.Tasks[0].Result != nil })

	if settled.Status != operations.Cancelled || len(settled.Tasks) != 2 || settled.Tasks[1].Status != operations.TaskCancelled || len(settled.Tasks[0].Result.NextActions) != 5 {
		t.Fatalf("late completion changed cancelled work: %+v", settled)
	}
	select {
	case path := <-entered:
		t.Fatalf("unexpected request after cancellation: %s", path)
	default:
	}
	a.fails("assign_task", object{"operation_id": op.ID, "action_name": "check-seo", "input": object{"url": url}}, "finished")
	release("/blocker")
	a.wait(blocker.ID, allTasksSucceeded)
	a.call("cancel_operation", object{"operation_id": blocker.ID}, nil)
}

func allTasksSucceeded(op operationDetail) bool {
	if len(op.Tasks) == 0 {
		return false
	}
	for _, task := range op.Tasks {
		if task.Status != operations.TaskSucceeded {
			return false
		}
	}
	return true
}

func heldWebsite(t *testing.T, paths ...string) (string, <-chan string, func(string)) {
	t.Helper()
	gates := map[string]chan struct{}{}
	for _, path := range paths {
		gates[path] = make(chan struct{})
	}
	entered := make(chan string, 32)
	url := serveWebsite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the first request per held path is signalled; browser requests after
		// release are ordinary page loads. Other paths reveal accidental dispatch.
		if gate := gates[r.URL.Path]; gate != nil {
			select {
			case <-gate:
			default:
				entered <- r.URL.Path
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
			}
		} else if r.URL.Path == "/queued" {
			entered <- r.URL.Path
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<!doctype html><html lang=en><title>%s</title><h1>Review fixture</h1></html>", strings.TrimPrefix(r.URL.Path, "/"))
	}))
	release := func(path string) {
		select {
		case <-gates[path]:
		default:
			close(gates[path])
		}
	}
	t.Cleanup(func() {
		for _, path := range paths {
			release(path)
		}
	})
	return url, entered, release
}

func TestOperationWebsiteWorkflow(t *testing.T) {
	for _, scenario := range []struct {
		mission, discovery string
		checks             []string
	}{
		{"review-website-no-browser", "discover-pages-no-browser", []string{"check-links", "check-seo", "detect-technologies"}},
		{"review-website-with-browser", "discover-pages", []string{"check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"}},
	} {
		t.Run(scenario.mission, func(t *testing.T) {
			testOperationWebsiteWorkflow(t, scenario.mission, scenario.discovery, scenario.checks)
		})
	}
}

func testOperationWebsiteWorkflow(t *testing.T, mission, discovery string, checks []string) {
	const reportSummary = "MCP API website workflow passed"
	a := connect(t)
	url := website(t)
	var op operationDetail
	a.call("start_operation", object{"mission_name": mission, "input": object{"url": url, "max_pages": 2}}, &op)
	assertMissionPreflight(t, a, op, 1+len(checks))
	if op.Progress.TotalTasks != 0 || op.Progress.FinishedTasks != 0 || op.Progress.Settled || op.Progress.TaskCounts == nil {
		t.Fatalf("new operation progress: %+v", op.Progress)
	}
	a.fails("assign_task", object{"operation_id": op.ID, "step_id": discovery, "action_name": discovery, "input": object{"url": url, "max_pages": 2}},
		fmt.Sprintf(`mission %q declares no steps; omit step_id and use action_name`, mission))
	if rejected := a.get(op.ID); len(rejected.Tasks) != 0 {
		t.Fatalf("invalid step queued work: %+v", rejected.Tasks)
	}
	var root operations.Task
	a.call("assign_task", object{"operation_id": op.ID, "action_name": discovery, "input": object{"url": url, "max_pages": 2}}, &root)
	settled := a.wait(op.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	if settled.Preflight == nil || !settled.Preflight.Ready {
		t.Fatalf("ready mission lost preflight after execution: %+v", settled.Preflight)
	}
	if len(settled.Tasks) != 1+2*len(checks) {
		t.Fatalf("expected discovery + two pages of %d checks; got %d tasks", len(checks), len(settled.Tasks))
	}
	counts := map[string]int{}
	brokenLinks := false
	for _, task := range settled.Tasks {
		if task.Status != operations.TaskSucceeded || task.Result == nil {
			t.Fatalf("unsuccessful task: %+v", task)
		}
		coverage := slices.IndexFunc(op.Preflight.Actions, func(action operations.ActionReadiness) bool { return action.ActionName == task.ActionName })
		if coverage < 0 || !slices.Contains(op.Preflight.Actions[coverage].ReadyAgentIDs, task.AgentID) {
			t.Fatalf("controller selected an agent outside ready coverage: %+v", task)
		}
		counts[task.ActionName]++
		if mission == "review-website-no-browser" && len(task.Result.Artifacts) != 0 {
			t.Fatalf("browser-free review generated rendering artifacts: %+v", task.Result.Artifacts)
		}
		if task.ID != root.ID && task.ParentTaskID != root.ID {
			t.Fatalf("child lost parent: %+v", task)
		}
		if task.ID != root.ID && (task.Continuation == nil || task.Continuation.Status != operations.ContinuationResolved) {
			t.Fatalf("group member exposed a separate decision: %+v", task)
		}
		if task.ActionName == "capture-page" {
			if len(task.Result.Artifacts) != 1 {
				t.Fatalf("missing desktop/mobile screenshots: %+v", task.Result)
			}
			for _, ref := range task.Result.Artifacts {
				assertScreenshots(t, a, ref)
			}
		}
		if task.ActionName == "check-links" {
			var result struct {
				BrokenLinks int `json:"broken_links"`
			}
			if err := json.Unmarshal(task.Result.Output, &result); err != nil {
				t.Fatal(err)
			}
			brokenLinks = brokenLinks || result.BrokenLinks > 0
		}
	}
	for _, name := range checks {
		if counts[name] != 2 {
			t.Fatalf("%s count=%d", name, counts[name])
		}
	}
	if mission == "review-website-no-browser" && (counts["capture-page"] != 0 || counts["check-accessibility"] != 0 || counts["discover-pages"] != 0) {
		t.Fatalf("browser-free review queued browser workflow actions: %+v", counts)
	}
	if !brokenLinks {
		t.Fatal("fixture's broken link was not reported")
	}
	decision := modelDecision(settled)
	if decision.ID != root.ID || len(decision.Continuation.CreatedTaskIDs) != 2*len(checks) {
		t.Fatalf("expected one grouped decision on discovery, got %+v", decision)
	}
	a.resolve(op.ID, *decision, []object{})
	a.fails("resolve_continuation", object{"operation_id": op.ID, "task_id": decision.ID, "version": decision.Continuation.Version, "actions": []object{}}, "pending")
	a.fails("complete_operation", object{"operation_id": op.ID, "output": object{"summary": "missing required fields"}}, "output")
	var completed operationDetail
	a.call("complete_operation", object{"operation_id": op.ID, "output": object{"url": url, "summary": reportSummary, "findings": []object{{"page_url": url, "check": "links", "description": "Broken /missing link"}}}}, &completed)
	if completed.Status != operations.Completed || completed.ReportID == "" || !completed.Progress.Settled || completed.Progress.FinishedTasks != len(completed.Tasks) || completed.Progress.TotalTasks != len(completed.Tasks) {
		t.Fatalf("completion: %+v", completed)
	}
	var report reports.Report
	a.call("get_report", object{"operation_id": op.ID}, &report)
	if report.ID != completed.ReportID || report.OperationID != op.ID || !bytes.Contains(report.Output, []byte(reportSummary)) {
		t.Fatalf("report=%+v", report)
	}
	assertEvents(t, a, op.ID)
	a.fails("assign_task", object{"operation_id": op.ID, "action_name": "check-seo", "input": object{"url": url}}, "finished")
}

func TestOperationCancellationAndFailure(t *testing.T) {
	for _, method := range []string{"cancel_operation", "fail_operation"} {
		t.Run(method, func(t *testing.T) {
			a := connect(t)
			op := a.start("https://example.test")
			a.task(op.ID, "unavailable-test-action", "https://example.test")
			args := object{"operation_id": op.ID}
			if method == "fail_operation" {
				args["reason"] = "operator ended test"
			}
			var ended operationDetail
			a.call(method, args, &ended)
			expected := operations.Cancelled
			if method == "fail_operation" {
				expected = operations.Failed
			}
			if ended.Status != expected || len(ended.Tasks) != 1 || ended.Tasks[0].Status != operations.TaskCancelled || !ended.Progress.Settled || ended.Progress.FinishedTasks != 1 || ended.Progress.TotalTasks != 1 {
				t.Fatalf("terminal operation: %+v", ended)
			}
			a.fails("get_report", object{"operation_id": op.ID}, "report")
			a.fails("assign_task", object{"operation_id": op.ID, "action_name": "check-seo"}, "finished")
		})
	}
}

func assertScreenshots(t *testing.T, a *api, expected artifacts.Artifact) {
	t.Helper()
	uri := "missions://artifacts/" + expected.ID
	result, err := a.session.ReadResource(a.ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contents) != 1 || result.Contents[0].MIMEType != "application/json" {
		t.Fatal("artifact resource must return metadata")
	}
	var saved artifacts.Artifact
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Equal(expected) || len(saved.Files) != 2 || saved.Name != "Page screenshots" {
		t.Fatalf("artifact metadata lost ownership or grouped files: %+v", saved)
	}
	for i, file := range saved.Files {
		if file.Name != []string{"desktop.png", "mobile.png"}[i] {
			t.Fatalf("unexpected screenshot file: %+v", file)
		}
		resource, err := a.session.ReadResource(a.ctx, &mcp.ReadResourceParams{URI: uri + "/files/" + file.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(resource.Contents) != 1 || resource.Contents[0].MIMEType != "image/png" || !bytes.HasPrefix(resource.Contents[0].Blob, []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatal("file resource is not a PNG screenshot")
		}
		if int64(len(resource.Contents[0].Blob)) != file.Size {
			t.Fatal("file size does not match saved metadata")
		}
	}
}

func assertEvents(t *testing.T, a *api, id string) {
	t.Helper()
	expected := a.get(id).Events
	if len(expected) < 3 || expected[len(expected)-1].Type != operations.OperationCompleted {
		t.Fatalf("workflow did not produce events for pagination: %+v", expected)
	}
	var result struct {
		Events     []operations.Event `json:"events"`
		NextCursor string             `json:"next_cursor"`
	}
	a.call("operation_events", object{"operation_id": id}, &result)
	defaultCount := min(len(expected), 100)
	if !reflect.DeepEqual(result.Events, expected[:defaultCount]) || result.NextCursor == "" {
		t.Fatalf("default event page: %+v", result)
	}
	defaultCursor := result.NextCursor
	var cursor string
	var lastSequence uint64
	var events []operations.Event
	for len(events) < len(expected) {
		result.Events = nil
		a.call("operation_events", object{"operation_id": id, "cursor": cursor, "limit": 2}, &result)
		if len(result.Events) == 0 || len(result.Events) > 2 || result.NextCursor == "" || result.NextCursor == cursor {
			t.Fatalf("invalid page after %q: %+v", cursor, result)
		}
		for _, event := range result.Events {
			if event.Sequence <= lastSequence {
				t.Fatalf("events repeated or out of order after %q: %+v", cursor, result)
			}
			lastSequence = event.Sequence
		}
		cursor = result.NextCursor
		events = append(events, result.Events...)
	}
	if !reflect.DeepEqual(events, expected) {
		t.Fatalf("pagination lost or repeated events: got %+v, want %+v", events, expected)
	}
	if len(expected) <= 100 && cursor != defaultCursor {
		t.Fatalf("cursor depends on page size: default %q, paginated %q", defaultCursor, cursor)
	}
	result.Events = nil
	a.call("operation_events", object{"operation_id": id, "cursor": cursor, "limit": 2, "wait_seconds": 1}, &result)
	if len(result.Events) != 0 || result.NextCursor != cursor {
		t.Fatalf("empty page must preserve cursor %q: %+v", cursor, result)
	}
	assertInvalidPageLimits(t, "operation_events", id)
	a.fails("operation_events", object{"operation_id": id, "cursor": "invalid"}, "cursor")
	a.fails("operation_events", object{"operation_id": "ffffffff-ffff-4fff-8fff-ffffffffffff", "limit": 2}, "unknown operation")
}

func assertInvalidPageLimits(t *testing.T, tool, operationID string) {
	t.Helper()
	for _, limit := range []int{0, -1, 1001} {
		invalid := openSession(t)
		_, err := invalid.session.CallTool(invalid.ctx, &mcp.CallToolParams{Name: tool, Arguments: object{"operation_id": operationID, "limit": limit}})
		if err == nil || !strings.Contains(err.Error(), "limit") {
			t.Errorf("%s invalid limit %d: expected argument error, got %v", tool, limit, err)
		}
	}
}

func TestOperationDecisionAfterActionFailure(t *testing.T) {
	a := connect(t)
	url := website(t)
	op := a.start(url)
	a.task(op.ID, "check-seo", url+"/missing")
	failed := a.wait(op.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	parent := modelDecision(failed)
	if parent.Status != operations.TaskFailed || parent.Error == "" {
		t.Fatalf("failure evidence missing: %+v", parent)
	}

	assertAgentSelectionRejected(t, "resolve_continuation", object{"operation_id": op.ID, "task_id": parent.ID, "version": parent.Continuation.Version,
		"actions": []object{{"action_name": "check-seo", "agent_id": parent.AgentID, "input": object{"url": url}}}})
	unchanged := a.get(op.ID)
	pending := modelDecision(unchanged)
	if len(unchanged.Tasks) != 1 || pending == nil || pending.Continuation.Version != parent.Continuation.Version {
		t.Fatalf("rejected agent selection changed the decision: %+v", unchanged)
	}

	decision := object{"operation_id": op.ID, "task_id": parent.ID, "version": parent.Continuation.Version,
		"actions": []object{{"action_name": "check-seo", "input": object{"url": url}, "reason": "Review the available page"}}}
	a.call("resolve_continuation", decision, nil)
	a.fails("resolve_continuation", decision, "pending")
	continued := a.wait(op.ID, func(op operationDetail) bool { return len(op.Tasks) == 2 && modelDecision(op) != nil })

	child := modelDecision(continued)
	if child.ParentTaskID != parent.ID || child.Status != operations.TaskSucceeded {
		t.Fatalf("follow-up task: %+v", child)
	}
	a.resolve(op.ID, *child, []object{})
	a.call("complete_operation", object{"operation_id": op.ID, "output": object{"url": url, "summary": "Available page reviewed", "findings": []object{}, "limitations": []string{"Missing page returned HTTP 404"}}}, nil)
	if a.get(op.ID).Status != operations.Completed {
		t.Fatal("operation did not complete")
	}
}

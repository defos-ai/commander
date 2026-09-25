package mcp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConcurrentOperationsStayIsolated(t *testing.T) {
	// Hold both requests so capacity and isolation are observable without timing races.
	a := connect(t)
	b := openSession(t)
	url, entered, release := heldWebsite(t, "/first", "/second")
	first, second := a.start(url+"/first"), b.start(url+"/second")
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
	queued := a.task(first.ID, "check-seo", url+"/extra")
	if queued.Status != operations.TaskQueued {
		t.Fatalf("third task exceeded the two agents' capacity: %+v", queued)
	}
	var question operations.Question
	a.call("ask_question", object{"operation_id": first.ID, "text": "Approve the first review?", "options": []string{"Yes", "No"}}, &question)

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
	for _, op := range []operationDetail{left, right} {
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
			if task.ActionName == "capture-page" && len(task.Result.Artifacts) != 2 {
				t.Fatalf("missing screenshots: %+v", task.Result)
			}
			for _, ref := range task.Result.Artifacts {
				if ref.CommandID != task.CommandID || ref.AgentID != task.AgentID || artifactOwners[ref.ID] != "" {
					t.Fatalf("incorrect artifact ownership: %+v", ref)
				}
				artifactOwners[ref.ID] = op.ID
				assertPNG(t, a, ref.ID)
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
	if len(left.Questions) != 1 || left.Questions[0].Answer != "Yes" || len(right.Questions) != 0 || len(artifactOwners) != 4 {
		t.Fatal("questions or artifacts leaked between operations")
	}
}

func TestCancellationWhileActionIsRunning(t *testing.T) {
	a := connect(t)
	url, entered, release := heldWebsite(t, "/slow")
	op := a.start(url + "/slow")
	a.call("assign_task", object{"operation_id": op.ID, "action_name": "discover-pages", "input": object{"url": url + "/slow", "max_pages": 1}}, nil)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("discovery did not start")
	}
	running := a.get(op.ID).Tasks[0]
	a.call("assign_task", object{"operation_id": op.ID, "agent_id": running.AgentID, "action_name": "check-seo", "input": object{"url": url + "/queued"}}, nil)

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
	const reportSummary = "MCP API website workflow passed"
	a := connect(t)
	url := website(t)
	op := a.start(url)
	a.fails("assign_task", object{"operation_id": op.ID, "step_id": "discover-pages", "action_name": "discover-pages", "input": object{"url": url, "max_pages": 2}},
		`mission "review-website" declares no steps; omit step_id and use action_name`)
	if rejected := a.get(op.ID); len(rejected.Tasks) != 0 {
		t.Fatalf("invalid step queued work: %+v", rejected.Tasks)
	}
	var root operations.Task
	a.call("assign_task", object{"operation_id": op.ID, "action_name": "discover-pages", "input": object{"url": url, "max_pages": 2}}, &root)
	settled := a.wait(op.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	if len(settled.Tasks) != 11 {
		t.Fatalf("expected discovery + 2×5 checks; got %d", len(settled.Tasks))
	}
	counts := map[string]int{}
	brokenLinks := false
	for _, task := range settled.Tasks {
		if task.Status != operations.TaskSucceeded || task.Result == nil {
			t.Fatalf("unsuccessful task: %+v", task)
		}
		counts[task.ActionName]++
		if task.ID != root.ID && task.ParentTaskID != root.ID {
			t.Fatalf("child lost parent: %+v", task)
		}
		if task.ActionName == "capture-page" {
			if len(task.Result.Artifacts) != 2 {
				t.Fatalf("missing desktop/mobile screenshots: %+v", task.Result)
			}
			for _, ref := range task.Result.Artifacts {
				assertPNG(t, a, ref.ID)
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
	for _, name := range []string{"check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"} {
		if counts[name] != 2 {
			t.Fatalf("%s count=%d", name, counts[name])
		}
	}
	if !brokenLinks {
		t.Fatal("fixture's broken link was not reported")
	}
	decision := modelDecision(settled)
	if decision.ID != root.ID {
		t.Fatalf("expected one grouped decision on discovery, got %+v", decision)
	}
	a.resolve(op.ID, *decision, []object{})
	a.fails("resolve_continuation", object{"operation_id": op.ID, "task_id": decision.ID, "version": decision.Continuation.Version, "actions": []object{}}, "pending")
	a.fails("complete_operation", object{"operation_id": op.ID, "output": object{"summary": "missing required fields"}}, "output")
	var completed operations.Operation
	a.call("complete_operation", object{"operation_id": op.ID, "output": object{"url": url, "summary": reportSummary, "findings": []object{{"page_url": url, "check": "links", "description": "Broken /missing link"}}}}, &completed)
	if completed.Status != operations.Completed || completed.ReportID == "" {
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
			var ended operations.Operation
			a.call(method, args, &ended)
			expected := operations.Cancelled
			if method == "fail_operation" {
				expected = operations.Failed
			}
			if ended.Status != expected || len(ended.Tasks) != 1 || ended.Tasks[0].Status != operations.TaskCancelled {
				t.Fatalf("terminal operation: %+v", ended)
			}
			a.fails("get_report", object{"operation_id": op.ID}, "report")
			a.fails("assign_task", object{"operation_id": op.ID, "action_name": "check-seo"}, "finished")
		})
	}
}

func assertPNG(t *testing.T, a *api, id string) {
	t.Helper()
	result, err := a.session.ReadResource(a.ctx, &mcp.ReadResourceParams{URI: "missions://artifacts/" + id})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contents) != 1 || result.Contents[0].MIMEType != "image/png" || !bytes.HasPrefix(result.Contents[0].Blob, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("resource is not a PNG screenshot")
	}
}
func assertEvents(t *testing.T, a *api, id string) {
	t.Helper()
	var result struct {
		Events       []operations.Event `json:"events"`
		NextSequence uint64             `json:"next_sequence"`
	}
	a.call("operation_events", object{"operation_id": id}, &result)
	if len(result.Events) == 0 || result.Events[len(result.Events)-1].Type != operations.OperationCompleted {
		t.Fatalf("missing completion event: %+v", result)
	}
	for i, event := range result.Events {
		if event.Sequence != uint64(i+1) {
			t.Fatalf("event gap: %+v", event)
		}
	}
	cursor := result.NextSequence
	a.call("operation_events", object{"operation_id": id, "after_sequence": cursor, "wait_seconds": 1}, &result)
	if len(result.Events) != 0 || result.NextSequence != cursor {
		t.Fatalf("repeated events: %+v", result)
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

package mcp_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOperationWebsiteWorkflow(t *testing.T) {
	// Arrange: public MCP only, real controller/agents and a deterministic website.
	const reportSummary = "MCP API website workflow passed"
	a := connect(t)
	url := website(t)
	op := a.start(url)
	// Act: assign one discovery; the mission queues all ten child checks.
	var root operations.Task
	a.call("assign_task", object{"operation_id": op.ID, "action_name": "discover-pages", "input": object{"url": url, "max_pages": 2}}, &root)
	settled := a.wait(op.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	// Assert: both pages ran every check, real screenshots can be read over MCP.
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
	// Final reports are immutable through commands.
	a.fails("assign_task", object{"operation_id": op.ID, "action_name": "check-seo", "input": object{"url": url}}, "finished")
}

func TestOperationCancellationAndFailure(t *testing.T) {
	for _, method := range []string{"cancel_operation", "fail_operation"} {
		t.Run(method, func(t *testing.T) {
			a := connect(t)
			// Arrange: queued work requiring an action no current agent can run.
			op := a.start("https://example.test")
			a.task(op.ID, "unavailable-test-action", "https://example.test")
			args := object{"operation_id": op.ID}
			if method == "fail_operation" {
				args["reason"] = "operator ended test"
			}
			// Act.
			var ended operations.Operation
			a.call(method, args, &ended)
			// Assert.
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
	// Arrange: an action that will observe an actual HTTP failure.
	a := connect(t)
	url := website(t)
	op := a.start(url)
	a.task(op.ID, "check-seo", url+"/missing")
	failed := a.wait(op.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	parent := modelDecision(failed)
	if parent.Status != operations.TaskFailed || parent.Error == "" {
		t.Fatalf("failure evidence missing: %+v", parent)
	}

	// Act: a client/model chooses another action through the versioned decision.
	decision := object{"operation_id": op.ID, "task_id": parent.ID, "version": parent.Continuation.Version,
		"actions": []object{{"action_name": "check-seo", "input": object{"url": url}, "reason": "Review the available page"}}}
	a.call("resolve_continuation", decision, nil)
	a.fails("resolve_continuation", decision, "pending")
	continued := a.wait(op.ID, func(op operationDetail) bool { return len(op.Tasks) == 2 && modelDecision(op) != nil })

	// Assert: only one child was queued, its result is preserved, and the report can finish.
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

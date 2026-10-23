package mcp_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConcurrentOperationsStayIsolated(t *testing.T) {
	a := connect(t)
	b := openSession(t)
	texts := []string{"First isolated operation", "Second independent operation"}
	first, second := a.start(texts[0]), b.start(texts[1])
	var empty struct {
		Tasks      []operations.Task `json:"tasks"`
		NextCursor *string           `json:"next_cursor"`
	}
	a.call("operation_tasks", object{"operation_id": first.ID}, &empty)
	if empty.Tasks == nil || len(empty.Tasks) != 0 || empty.NextCursor == nil || *empty.NextCursor != "" {
		t.Fatalf("new operation must have an empty task list: %+v", empty)
	}
	// A real operator question blocks one operation while the other completes.
	var question operations.Question
	a.call("ask_question", object{"operation_id": first.ID, "text": "Analyze the first text?", "options": []string{"Yes", "No"}}, &question)
	a.fails("assign_task", object{"operation_id": first.ID, "action_name": "analyze-text", "input": object{"text": texts[0]}}, "pending question")
	b.task(second.ID, "analyze-text", object{"text": texts[1]})
	right := b.wait(second.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	left := a.get(first.ID)
	if len(left.Tasks) != 0 || len(left.Questions) != 1 || len(right.Questions) != 0 || !allTasksSucceeded(right) {
		t.Fatalf("one operation's question affected the other: first=%+v second=%+v", left, right)
	}
	a.call("answer_question", object{"operation_id": first.ID, "question_id": question.ID, "answer": "Yes"}, nil)
	a.task(first.ID, "analyze-text", object{"text": texts[0]})
	left = a.wait(first.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	if len(left.Tasks) != 2 || len(right.Tasks) != 2 {
		t.Fatalf("expected a root and proposed child per operation: first=%+v second=%+v", left.Tasks, right.Tasks)
	}
	a.fails("get_task", object{"operation_id": first.ID, "task_id": right.Tasks[0].ID}, "unknown task")
	a.fails("get_task", object{"operation_id": first.ID, "task_id": id.New()}, "unknown task")
	a.fails("get_task", object{"operation_id": first.ID, "task_id": "not-a-uuid"}, "UUID")
	a.fails("get_task", object{"operation_id": id.OperationID(), "task_id": left.Tasks[0].ID}, "unknown operation")
	a.fails("operation_tasks", object{"operation_id": id.OperationID()}, "unknown operation")
	a.fails("operation_tasks", object{"operation_id": first.ID, "cursor": right.Tasks[0].ID}, "unknown task")
	a.fails("operation_tasks", object{"operation_id": first.ID, "cursor": "invalid"}, "UUID")
	assertInvalidPageLimits(t, "operation_tasks", first.ID)

	artifactOwners := map[string]string{}
	var previousArtifact *artifacts.Artifact
	for index, op := range []operationDetail{left, right} {
		assertTaskQueries(t, a, op)
		root, child := op.Tasks[0], op.Tasks[1]
		var input struct{ Text string }
		if err := json.Unmarshal(root.Input, &input); err != nil || input.Text != texts[index] {
			t.Fatalf("task input crossed operation boundaries: %s (%v)", root.Input, err)
		}
		var proposedInput, savedInput object
		if root.Result == nil || child.Result == nil {
			t.Fatalf("operation lost result evidence: %+v", op.Tasks)
		}
		if err := json.Unmarshal(root.Result.Output, &proposedInput); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(child.Input, &savedInput); err != nil || !reflect.DeepEqual(savedInput, proposedInput) || child.ParentTaskID != root.ID {
			t.Fatalf("child lost its parent's proposed input: %+v (%v)", child, err)
		}
		if len(child.Result.Artifacts) != 1 {
			t.Fatalf("summary artifact missing: %+v", child.Result)
		}
		for _, task := range op.Tasks {
			if task.OperationID != op.ID || task.Status != operations.TaskSucceeded {
				t.Fatalf("task crossed operation boundaries: %+v", task)
			}
			for _, ref := range task.Result.Artifacts {
				if ref.CommandID != task.CommandID || ref.AgentID != task.AgentID || artifactOwners[ref.ID] != "" {
					t.Fatalf("incorrect artifact ownership: %+v", ref)
				}
				artifactOwners[ref.ID] = op.ID
				assertArtifactResource(t, a, ref)
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
		a.resolve(op.ID, *modelDecision(op), []object{})
		summary := "Analysis " + op.ID
		a.call("complete_operation", object{"operation_id": op.ID, "output": object{"summary": summary}}, nil)
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

func TestOperationTextWorkflow(t *testing.T) {
	const reportSummary = "MCP text workflow passed"
	const text = "Analyze this fixture and save its summary."
	a := connect(t)
	var op operationDetail
	a.call("start_operation", object{"mission_name": "analyze-text", "input": object{"text": text}}, &op)
	assertMissionPreflight(t, a, op, 2)
	if op.Progress.TotalTasks != 0 || op.Progress.FinishedTasks != 0 || op.Progress.Settled || op.Progress.TaskCounts == nil {
		t.Fatalf("new operation progress: %+v", op.Progress)
	}
	a.fails("assign_task", object{"operation_id": op.ID, "step_id": "analyze-text", "action_name": "analyze-text", "input": object{"text": text}},
		`mission "analyze-text" declares no steps; omit step_id and use action_name`)
	if rejected := a.get(op.ID); len(rejected.Tasks) != 0 {
		t.Fatalf("invalid step queued work: %+v", rejected.Tasks)
	}
	root := a.task(op.ID, "analyze-text", object{"text": text})
	settled := a.wait(op.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	if settled.Preflight == nil || !settled.Preflight.Ready || len(settled.Tasks) != 2 {
		t.Fatalf("ready mission lost preflight or proposed work: %+v", settled)
	}
	for _, task := range settled.Tasks {
		if task.Status != operations.TaskSucceeded || task.Result == nil {
			t.Fatalf("unsuccessful task: %+v", task)
		}
		coverage := slices.IndexFunc(op.Preflight.Actions, func(action operations.ActionReadiness) bool { return action.ActionName == task.ActionName })
		if coverage < 0 || !slices.Contains(op.Preflight.Actions[coverage].ReadyAgentIDs, task.AgentID) {
			t.Fatalf("controller selected an agent outside ready coverage: %+v", task)
		}
		if task.ID != root.ID && (task.ParentTaskID != root.ID || task.Continuation == nil || task.Continuation.Status != operations.ContinuationResolved) {
			t.Fatalf("group member lost its parent or exposed a separate decision: %+v", task)
		}
	}
	child := settled.Tasks[1]
	if child.ActionName != "write-summary" || len(child.Result.Artifacts) != 1 {
		t.Fatalf("proposed action lost its artifact: %+v", child)
	}
	assertArtifactResource(t, a, child.Result.Artifacts[0])
	decision := modelDecision(settled)
	if decision.ID != root.ID || !slices.Equal(decision.Continuation.CreatedTaskIDs, []string{child.ID}) {
		t.Fatalf("expected one grouped decision on the root, got %+v", decision)
	}
	a.resolve(op.ID, *decision, []object{})
	a.fails("resolve_continuation", object{"operation_id": op.ID, "task_id": decision.ID, "version": decision.Continuation.Version, "actions": []object{}}, "pending")
	a.fails("complete_operation", object{"operation_id": op.ID, "output": object{}}, "output")
	var completed operationDetail
	a.call("complete_operation", object{"operation_id": op.ID, "output": object{"summary": reportSummary}}, &completed)
	if completed.Status != operations.Completed || completed.ReportID == "" || !completed.Progress.Settled || completed.Progress.FinishedTasks != 2 || completed.Progress.TotalTasks != 2 {
		t.Fatalf("completion: %+v", completed)
	}
	var report reports.Report
	a.call("get_report", object{"operation_id": op.ID}, &report)
	if report.ID != completed.ReportID || report.OperationID != op.ID || !bytes.Contains(report.Output, []byte(reportSummary)) {
		t.Fatalf("report=%+v", report)
	}
	assertEvents(t, a, op.ID)
	a.fails("assign_task", object{"operation_id": op.ID, "action_name": "write-summary", "input": summaryInput()}, "finished")
}

func TestOperationCancellationAndFailure(t *testing.T) {
	for _, method := range []string{"cancel_operation", "fail_operation"} {
		t.Run(method, func(t *testing.T) {
			a := connect(t)
			op := a.start("Cancelled or failed fixture")
			a.task(op.ID, "unavailable-test-action", object{})
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
			a.fails("assign_task", object{"operation_id": op.ID, "action_name": "write-summary"}, "finished")
		})
	}
}

func assertArtifactResource(t *testing.T, a *api, expected artifacts.Artifact) {
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
	if !saved.Equal(expected) || len(saved.Files) != 1 {
		t.Fatalf("artifact metadata lost ownership or files: %+v", saved)
	}
	for _, file := range saved.Files {
		resource, err := a.session.ReadResource(a.ctx, &mcp.ReadResourceParams{URI: uri + "/files/" + file.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(resource.Contents) != 1 || resource.Contents[0].MIMEType != file.MediaType || resource.Contents[0].Text == "" {
			t.Fatal("text file resource lost content or media type")
		}
		data := []byte(resource.Contents[0].Text)
		digest := sha256.Sum256(data)
		if int64(len(data)) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 {
			t.Fatal("file bytes do not match saved size and digest")
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
	op := a.start("Recover an invalid action input")
	// The JSON matches the action schema, but execution rejects inconsistent counts.
	a.task(op.ID, "write-summary", object{"character_count": 1, "word_count": 0, "line_count": 1})
	failed := a.wait(op.ID, func(op operationDetail) bool { return modelDecision(op) != nil })
	parent := modelDecision(failed)
	if parent.Status != operations.TaskFailed || parent.Error == "" {
		t.Fatalf("failure evidence missing: %+v", parent)
	}

	assertAgentSelectionRejected(t, "resolve_continuation", object{"operation_id": op.ID, "task_id": parent.ID, "version": parent.Continuation.Version,
		"actions": []object{{"action_name": "write-summary", "agent_id": parent.AgentID, "input": summaryInput()}}})
	unchanged := a.get(op.ID)
	pending := modelDecision(unchanged)
	if len(unchanged.Tasks) != 1 || pending == nil || pending.Continuation.Version != parent.Continuation.Version {
		t.Fatalf("rejected agent selection changed the decision: %+v", unchanged)
	}

	decision := object{"operation_id": op.ID, "task_id": parent.ID, "version": parent.Continuation.Version,
		"actions": []object{{"action_name": "write-summary", "input": summaryInput(), "reason": "Retry with valid analysis counts"}}}
	a.call("resolve_continuation", decision, nil)
	a.fails("resolve_continuation", decision, "pending")
	continued := a.wait(op.ID, func(op operationDetail) bool { return len(op.Tasks) == 2 && modelDecision(op) != nil })

	child := modelDecision(continued)
	if child.ParentTaskID != parent.ID || child.Status != operations.TaskSucceeded {
		t.Fatalf("follow-up task: %+v", child)
	}
	a.resolve(op.ID, *child, []object{})
	a.call("complete_operation", object{"operation_id": op.ID, "output": object{"summary": "Recovered the failed action and reviewed its evidence"}}, nil)
	if a.get(op.ID).Status != operations.Completed {
		t.Fatal("operation did not complete")
	}
}

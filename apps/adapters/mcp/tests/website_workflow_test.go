package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"commander/framework/domain/missions"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type websiteOperation struct {
	ID, Status string
	Tasks      []websiteTask
}

type websiteTask struct {
	ID, Status   string
	ParentTaskID string `json:"parent_task_id"`
	ActionName   string `json:"action_name"`
	AgentID      string `json:"agent_id"`
	Input        json.RawMessage
	Result       *struct {
		Output    json.RawMessage
		Artifacts []struct{ ID, Name string }
	}
	Continuation *struct {
		Version         uint64
		DecisionBy      string `json:"decision_by"`
		Status          string
		CreatedTaskIDs  []string          `json:"created_task_ids"`
		ProposedActions []json.RawMessage `json:"proposed_actions"`
		QuestionID      string            `json:"question_id"`
	}
}

func TestWebsiteDiscoveryModelHandoff(t *testing.T) {
	// Arrange: without mission automation, discovery proposals await a model decision.
	d := newDeployment(t)
	seedMission(t, d)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!doctype html><title>Local website</title><h1>Welcome</h1>`)
	}))
	defer fixture.Close()
	endpoint, _ := d.mcp(t, false)
	d.start(t, agentBinary, d.env, "--actions=discover-pages,check-links", "--permissions=network-access", "--heartbeat-interval=100ms")
	session := connect(t, d.ctx, endpoint)
	readyAgents(t, d, session, 1)
	operation := startMission(t, d, session)

	// Act: run discovery, then replace the controller before resolving its proposals.
	task := decode[websiteTask](t, call(t, d.ctx, session, "assign_task", map[string]any{
		"operation_id": operation.ID, "action_name": "discover-pages", "input": map[string]any{"url": fixture.URL, "max_pages": 1},
	}))
	waitTask(t, d, session, operation.ID, task.ID, "succeeded")
	before := decode[websiteOperation](t, call(t, d.ctx, session, "get_operation", map[string]any{"operation_id": operation.ID}))
	d.controllerProcess.interrupt(t)
	d.controller(t)
	after := decode[websiteOperation](t, call(t, d.ctx, session, "get_operation", map[string]any{"operation_id": operation.ID}))

	// Assert: all five checks are proposals, not silently dispatched work; restart preserves them.
	if len(after.Tasks) != 1 || !reflect.DeepEqual(before, after) {
		t.Fatalf("discovery changed across restart: before=%+v after=%+v", before, after)
	}
	c := after.Tasks[0].Continuation
	if after.Status != "planning" || c == nil || c.DecisionBy != "model" || c.Status != "pending" || len(c.CreatedTaskIDs) != 0 || len(c.ProposedActions) != 5 {
		t.Fatalf("expected discovery decision: operation=%+v continuation=%+v", after, c)
	}
	var proposed []string
	for _, raw := range c.ProposedActions {
		var action struct {
			ActionName string `json:"action_name"`
		}
		if err := json.Unmarshal(raw, &action); err != nil {
			t.Fatal(err)
		}
		proposed = append(proposed, action.ActionName)
	}
	if !slices.Equal(proposed, []string{"check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"}) {
		t.Fatalf("proposed checks: %v", proposed)
	}

	// Act: the model selects one check; stale retries must not create another task.
	resolution := map[string]any{"operation_id": operation.ID, "task_id": task.ID, "version": c.Version, "actions": []any{map[string]any{"action_name": "check-links", "input": map[string]any{"url": fixture.URL}}}}
	resolved := decode[websiteOperation](t, call(t, d.ctx, session, "resolve_continuation", resolution))
	rejected(t, d.ctx, session, "resolve_continuation", resolution)
	if len(resolved.Tasks) != 2 {
		t.Fatalf("expected one model-selected check: %+v", resolved)
	}
	waitTask(t, d, session, operation.ID, resolved.Tasks[1].ID, "succeeded")
	call(t, d.ctx, session, "complete_operation", map[string]any{"operation_id": operation.ID, "output": map[string]any{"summary": "Local links reviewed"}})
}

func websiteBrowser(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CHROMIUM_PATH"); path != "" {
		return path
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("Chromium is required for the full website workflow; set CHROMIUM_PATH")
	return ""
}

func TestWebsiteAutomaticReviewAndOperatorHandoff(t *testing.T) {
	// Arrange: the shipped mission, two real agents, and a local site with known issues.
	browser := websiteBrowser(t)
	d := newDeployment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	d.ctx = ctx
	catalog, err := filepath.Abs("../../../controllers/default/data/default-missions.json")
	if err != nil {
		t.Fatal(err)
	}
	d.controller(t, "--with-missions="+catalog)
	d.env["CHROMIUM_PATH"] = browser
	d.env["CHROMIUM_NO_SANDBOX"] = "true"
	endpoint, _ := d.mcp(t, false)
	for range 2 {
		d.start(t, agentBinary, d.env, "--permissions=network-access,execute-command", "--heartbeat-interval=100ms")
	}
	session := connect(t, d.ctx, endpoint)
	readyAgents(t, d, session, 2)
	listed := decode[struct{ Missions []missions.Mission }](t, call(t, d.ctx, session, "list_missions", map[string]any{}))
	index := slices.IndexFunc(listed.Missions, func(m missions.Mission) bool { return m.Name == "review-website" })
	if index < 0 || listed.Missions[index].Automations["discover-pages"].Continue != missions.WhenAll {
		t.Fatalf("missing website automation: %+v", listed)
	}

	released := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// The first two requests belong to discovery. Subsequent page checks wait
		// so the controller can restart with both queued and running work.
		if requests.Add(1) > 2 {
			select {
			case <-released:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			_, _ = io.WriteString(w, `<!doctype html><html lang="en"><title>Review fixture</title><meta name="generator" content="WordPress 6.6.2"><body><h1>Welcome</h1><a href="/about">About</a><a href="/missing">Broken link</a><img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=="></body></html>`)
		case "/about":
			_, _ = io.WriteString(w, `<!doctype html><html lang="en"><title>About</title><meta name="generator" content="WordPress 6.6.2"><body><h1>About this fixture</h1><a href="/">Home</a></body></html>`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "Page missing")
		}
	}))
	t.Cleanup(func() { release(); fixture.Close() })
	input := map[string]any{"url": fixture.URL, "max_pages": 2}
	operation := decode[websiteOperation](t, call(t, d.ctx, session, "start_operation", map[string]any{"mission_name": "review-website", "input": input}))
	parent := decode[websiteTask](t, call(t, d.ctx, session, "assign_task", map[string]any{"operation_id": operation.ID, "action_name": "discover-pages", "input": input}))
	read := func() websiteOperation {
		return decode[websiteOperation](t, call(t, d.ctx, session, "get_operation", map[string]any{"operation_id": operation.ID}))
	}
	poll(t, func() bool {
		op := read()
		queued, running := false, false
		for _, task := range op.Tasks {
			queued = queued || task.Status == "queued"
			running = running || task.Status == "running"
		}
		return len(op.Tasks) == 11 && queued && running
	})
	before := read()

	// Act: crash the controller during automatic fan-out, recover, and release page checks.
	d.controllerProcess.kill(t)
	d.controller(t)
	release()
	var finished websiteOperation
	deadline := time.Now().Add(60 * time.Second)
	for {
		finished = read()
		done := len(finished.Tasks) == 11
		for _, task := range finished.Tasks {
			done = done && task.Status != "queued" && task.Status != "running"
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("website checks did not finish: %+v", finished)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Assert: all checks execute automatically, both agents work, screenshots are readable,
	// and a single model decision collects the completed checks.
	used := map[string]bool{}
	screenshots := 0
	brokenLinks, missingImageAlt := 0, 0
	missingDescriptions, technologyVersions := 0, 0
	for _, task := range finished.Tasks {
		if task.Status != "succeeded" {
			t.Fatalf("website task failed: %+v", task)
		}
		if task.ID == parent.ID {
			parent = task
			if task.Continuation == nil || task.Continuation.Status != "pending" || task.Continuation.DecisionBy != "model" || len(task.Continuation.CreatedTaskIDs) != 10 {
				t.Fatalf("missing grouped decision: %+v", task)
			}
			continue
		}
		used[task.AgentID] = true
		if task.ParentTaskID != parent.ID || task.Continuation == nil || task.Continuation.Status != "resolved" {
			t.Fatalf("check escaped grouped review: %+v", task)
		}
		if task.Result == nil {
			t.Fatalf("check has no result: %+v", task)
		}
		switch task.ActionName {
		case "check-links":
			var observation struct {
				BrokenLinks int `json:"broken_links"`
			}
			if err := json.Unmarshal(task.Result.Output, &observation); err != nil {
				t.Fatal(err)
			}
			brokenLinks += observation.BrokenLinks
		case "check-accessibility":
			var observation struct {
				Issues []struct {
					Check string
					Count int
				}
			}
			if err := json.Unmarshal(task.Result.Output, &observation); err != nil {
				t.Fatal(err)
			}
			for _, issue := range observation.Issues {
				if issue.Check == "image-missing-alt" {
					missingImageAlt += issue.Count
				}
			}
		case "check-seo":
			var observation struct {
				Issues []struct{ Check string }
			}
			if err := json.Unmarshal(task.Result.Output, &observation); err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc(observation.Issues, func(issue struct{ Check string }) bool { return issue.Check == "missing-meta-description" }) {
				missingDescriptions++
			}
		case "detect-technologies":
			var observation struct {
				Indicators []struct{ Name, Version, Source string }
			}
			if err := json.Unmarshal(task.Result.Output, &observation); err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc(observation.Indicators, func(indicator struct{ Name, Version, Source string }) bool {
				return indicator.Name == "WordPress" && indicator.Version == "6.6.2" && indicator.Source == "meta-generator"
			}) {
				technologyVersions++
			}
		}
		if task.ActionName == "capture-page" {
			if task.Result == nil || len(task.Result.Artifacts) != 2 {
				t.Fatalf("missing desktop/mobile screenshots: %+v", task)
			}
			for _, artifact := range task.Result.Artifacts {
				resource, err := session.ReadResource(d.ctx, &mcp.ReadResourceParams{URI: "missions://artifacts/" + artifact.ID})
				if err != nil || len(resource.Contents) != 1 || len(resource.Contents[0].Blob) == 0 || resource.Contents[0].MIMEType != "image/png" {
					t.Fatalf("read screenshot: resource=%+v err=%v", resource, err)
				}
				screenshots++
			}
		}
	}
	if len(used) != 2 || screenshots != 4 || brokenLinks != 1 || missingImageAlt != 1 || missingDescriptions != 2 || technologyVersions != 2 {
		t.Fatalf("parallel evidence: agents=%d screenshots=%d broken_links=%d missing_alt=%d missing_descriptions=%d technology_versions=%d", len(used), screenshots, brokenLinks, missingImageAlt, missingDescriptions, technologyVersions)
	}
	for _, task := range before.Tasks {
		if !slices.ContainsFunc(finished.Tasks, func(current websiteTask) bool { return current.ID == task.ID }) {
			t.Fatalf("restart replaced task %s", task.ID)
		}
	}

	// Act: request operator guidance for the grouped review and restart while waiting.
	question := decode[struct{ ID string }](t, call(t, d.ctx, session, "ask_question", map[string]any{"operation_id": operation.ID, "task_id": parent.ID, "version": parent.Continuation.Version, "text": "Should the missing page block release?", "options": []string{"Yes", "No"}}))
	resolution := map[string]any{"operation_id": operation.ID, "task_id": parent.ID, "version": parent.Continuation.Version, "actions": []any{}}
	rejected(t, d.ctx, session, "resolve_continuation", resolution)
	waiting := read()
	if waiting.Status != "waiting" || waiting.Tasks[0].Continuation.QuestionID != question.ID || waiting.Tasks[0].Continuation.DecisionBy != "user" {
		t.Fatalf("question not linked: %+v", waiting)
	}
	d.controllerProcess.interrupt(t)
	d.controller(t)
	call(t, d.ctx, session, "answer_question", map[string]any{"operation_id": operation.ID, "question_id": question.ID, "answer": "Yes"})
	answered := read()
	if len(answered.Tasks) != 11 || answered.Tasks[0].Continuation.DecisionBy != "model" || answered.Tasks[0].Continuation.Status != "pending" {
		t.Fatalf("answer changed scheduled work: %+v", answered)
	}
	resolution["version"] = answered.Tasks[0].Continuation.Version
	call(t, d.ctx, session, "resolve_continuation", resolution)
	rejected(t, d.ctx, session, "resolve_continuation", resolution)
	call(t, d.ctx, session, "complete_operation", map[string]any{"operation_id": operation.ID, "output": map[string]any{"url": fixture.URL, "summary": "Fix the broken link before release.", "findings": []any{map[string]any{"page_url": fixture.URL, "check": "check-links", "description": "The missing page returns HTTP 404."}}}})
	report := decode[struct {
		OperationID string `json:"operation_id"`
		Mission     string
		Output      struct{ Summary string }
	}](t, call(t, d.ctx, session, "get_report", map[string]any{"operation_id": operation.ID}))
	if report.OperationID != operation.ID || report.Mission != "review-website" || report.Output.Summary != "Fix the broken link before release." {
		t.Fatalf("report lost review: %+v", report)
	}
}

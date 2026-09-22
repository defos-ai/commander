package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"commander/framework/domain/agents"
	"commander/framework/domain/operations"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type api struct {
	t       *testing.T
	ctx     context.Context
	session *mcp.ClientSession
}
type object = map[string]any

type operationDetail struct {
	operations.Operation
	LastQuestion       *operations.Question `json:"last_question"`
	RemainingQuestions int                  `json:"remaining_questions"`
}

func connect(t *testing.T) *api {
	t.Helper()
	// Clean persisted data before each test; the stack stays running.
	a := openSession(t)
	tools, err := a.session.ListTools(a.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	canClean := false
	for _, tool := range tools.Tools {
		canClean = canClean || tool.Name == "clean_up_state"
	}
	if !canClean {
		t.Fatal("test MCP must expose clean_up_state (ENV=tests); rebuild compose.test.yaml")
	}
	a.call("clean_up_state", object{}, nil)
	// Heartbeats repopulate live inventory after cleanup.
	deadline := time.Now().Add(30 * time.Second)
	for {
		var list struct {
			Agents []agents.Agent `json:"agents"`
		}
		a.call("list_agents", object{}, &list)
		if len(list.Agents) == 2 && list.Agents[0].Status == agents.AgentReady && list.Agents[1].Status == agents.AgentReady {
			break
		}
		if time.Now().After(deadline) {
			states := make([]string, 0, len(list.Agents))
			for _, agent := range list.Agents {
				states = append(states, fmt.Sprintf("%s: status=%s stale=%t last_heartbeat=%s", agent.ID, agent.Status, agent.Stale, agent.LastHeartbeat.Format(time.RFC3339)))
			}
			t.Fatalf("expected 2 ready agents after cleanup; found %d: [%s]; inspect controller logs for stopped event processing", len(list.Agents), strings.Join(states, "; "))
		}
		time.Sleep(100 * time.Millisecond)
	}
	var list struct {
		Operations []operations.Operation `json:"operations"`
	}
	a.call("list_operations", object{}, &list)
	if len(list.Operations) != 0 {
		t.Fatal("cleanup left operations from a previous test")
	}
	return a
}

func openSession(t *testing.T) *api {
	t.Helper()
	const endpoint = "http://127.0.0.1:14273/mcp"
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "commander-api-tests", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatalf("connect to MCP at %s: %v; start the test stack with docker compose -f compose.test.yaml up --build --wait", endpoint, err)
	}
	t.Cleanup(func() { session.Close() })
	return &api{t, ctx, session}
}

func (a *api) call(name string, args object, out any) {
	a.t.Helper()
	result, err := a.session.CallTool(a.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		a.t.Fatalf("%s: %v", name, err)
	}
	if result.IsError {
		a.t.Fatalf("%s: %s", name, resultText(result))
	}
	if out == nil {
		return
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		a.t.Fatal(err)
	}
	if string(raw) == "null" {
		raw = []byte(resultText(result))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		a.t.Fatalf("decode %s: %v; %s", name, err, raw)
	}
}
func resultText(r *mcp.CallToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if v, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, v.Text)
		}
	}
	return strings.Join(parts, "\n")
}
func (a *api) fails(name string, args object, contains string) {
	a.t.Helper()
	result, err := a.session.CallTool(a.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		a.t.Fatalf("%s returned transport error instead of tool error: %v", name, err)
	}
	if !result.IsError || !strings.Contains(strings.ToLower(resultText(result)), strings.ToLower(contains)) {
		a.t.Fatalf("%s: want error containing %q; got isError=%v, %q", name, contains, result.IsError, resultText(result))
	}
}
func (a *api) start(url string) operations.Operation {
	a.t.Helper()
	var op operations.Operation
	a.call("start_operation", object{"mission_name": "review-website", "input": object{"url": url, "max_pages": 2}}, &op)
	return op
}
func (a *api) get(id string) operationDetail {
	a.t.Helper()
	var op operationDetail
	a.call("get_operation", object{"operation_id": id}, &op)
	return op
}
func (a *api) wait(id string, done func(operationDetail) bool) operationDetail {
	a.t.Helper()
	var last operationDetail
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		last = a.get(id)
		if done(last) {
			return last
		}
		select {
		case <-deadline.C:
			raw, _ := json.Marshal(last)
			a.t.Fatalf("operation did not settle: %s", raw)
		case <-a.ctx.Done():
			a.t.Fatal(a.ctx.Err())
		case <-tick.C:
		}
	}
}
func (a *api) task(opID, action, url string) operations.Task {
	a.t.Helper()
	var task operations.Task
	a.call("assign_task", object{"operation_id": opID, "action_name": action, "input": object{"url": url}}, &task)
	return task
}
func modelDecision(op operationDetail) *operations.Task {
	for _, task := range op.Tasks {
		if c := task.Continuation; c != nil && c.Status == operations.ContinuationPending && c.DecisionBy == operations.DecisionModel {
			return &task
		}
	}
	return nil
}
func (a *api) resolve(opID string, task operations.Task, actions []object) {
	a.t.Helper()
	a.call("resolve_continuation", object{"operation_id": opID, "task_id": task.ID, "version": task.Continuation.Version, "actions": actions}, nil)
}

// The website lives on the test host. Containers reach it through host-gateway;
// no public websites, DNS services or model API credentials are needed.
func website(t *testing.T) string {
	t.Helper()
	return serveWebsite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Powered-By", "Express")
		fmt.Fprint(w, `<!doctype html><html lang="en"><head><title>Commander test site</title><meta name="generator" content="WordPress 6.8"></head><body><h1>Release review</h1><a href="/about">About</a><a href="/missing">Broken link</a><img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=="></body></html>`)
	}))
}

func serveWebsite(t *testing.T, handler http.Handler) string {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	host := os.Getenv("COMMANDER_TEST_WEBSITE_HOST")
	if host == "" {
		host = "host.docker.internal"
	}
	return "http://" + net.JoinHostPort(host, fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))
}

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
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
	Progress           operationProgress    `json:"progress"`
	Affinity           *operations.Affinity `json:"affinity"`
	LastQuestion       *operations.Question `json:"last_question"`
	RemainingQuestions int                  `json:"remaining_questions"`
}

type operationProgress struct {
	TaskCounts       map[string]int `json:"task_counts"`
	TotalTasks       int            `json:"total_tasks"`
	FinishedTasks    int            `json:"finished_tasks"`
	PendingDecisions int            `json:"pending_decisions"`
	PendingQuestions int            `json:"pending_questions"`
	Settled          bool           `json:"settled"`
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
		t.Fatal("test MCP must expose clean_up_state (ENV=tests); rebuild .github/composes/compose.test.yaml")
	}
	if err := a.settle(a.ctx); err != nil {
		t.Fatalf("settle previous test work: %v", err)
	}
	a.call("clean_up_state", object{}, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		session, err := newSession(ctx)
		if err != nil {
			t.Errorf("connect for test teardown: %v", err)
			return
		}
		defer session.Close()
		if err := (&api{t: t, ctx: ctx, session: session}).settle(ctx); err != nil {
			t.Errorf("settle test work: %v", err)
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		var list struct {
			Agents []agents.Agent `json:"agents"`
		}
		a.call("list_agents", object{}, &list)
		ready := true
		types := map[string]int{}
		for _, agent := range list.Agents {
			types[string(agent.Type)]++
			expected := expectedAgentActions(string(agent.Type))
			ready = ready && len(expected) > 0 && !agent.Stale && agent.Status == agents.AgentReady && agent.Work == nil && len(agent.Actions) == len(expected)
			for _, action := range agent.Actions {
				ready = ready && (action.Name == "build-agent" || action.Name == "publish-agent" || action.Name == "release-agent" || action.Status == "ready")
			}
		}
		if ready && types["example-agent"] == 2 && types["builder-agent"] == 1 && types["spawner-agent"] == 1 {
			break
		}
		if time.Now().After(deadline) {
			states := make([]string, 0, len(list.Agents))
			for _, agent := range list.Agents {
				states = append(states, fmt.Sprintf("%s: status=%s stale=%t last_heartbeat=%s", agent.ID, agent.Status, agent.Stale, agent.LastHeartbeat.Format(time.RFC3339)))
			}
			t.Fatalf("expected two example agents, one builder and one spawner ready after cleanup; found %d: [%s]; inspect controller logs for stopped event processing", len(list.Agents), strings.Join(states, "; "))
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

func expectedAgentActions(agentType string) []string {
	switch agentType {
	case "example-agent":
		return []string{"analyze-text", "write-summary"}
	case "spawner-agent":
		return []string{"spawn-agent"}
	case "builder-agent":
		return []string{"build-agent", "checkout-template", "publish-agent", "release-agent"}
	case "fixture-agent":
		return []string{"fixture-echo"}
	default:
		return nil
	}
}

func openSession(t *testing.T) *api {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	session, err := newSession(ctx)
	if err != nil {
		t.Fatalf("connect to test MCP: %v; start .github/composes/compose.test.yaml or .github/composes/compose.full.test.yaml", err)
	}
	t.Cleanup(func() { session.Close() })
	return &api{t, ctx, session}
}

func newSession(ctx context.Context) (*mcp.ClientSession, error) {
	token := os.Getenv("COMMANDER_TEST_MCP_TOKEN")
	if token == "" {
		token = "mcp-fixture-token"
	}
	client := &http.Client{
		Timeout:       90 * time.Second,
		Transport:     bearerTransport(token),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return mcp.NewClient(&mcp.Implementation{Name: "commander-mcp-tests", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: mcpEndpoint(), HTTPClient: client, DisableStandaloneSSE: true,
	}, nil)
}

func mcpEndpoint() string {
	endpoint := os.Getenv("COMMANDER_TEST_MCP_URL")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:14273/mcp"
	}
	return endpoint
}

type bearerTransport string

func (transport bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+string(transport))
	return http.DefaultTransport.RoundTrip(request)
}

func (a *api) settle(ctx context.Context) error {
	call := func(name string, args object, out any) error {
		result, err := a.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return err
		}
		if result.IsError {
			return fmt.Errorf("%s: %s", name, resultText(result))
		}
		if out != nil {
			return json.Unmarshal([]byte(resultText(result)), out)
		}
		return nil
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var list struct {
			Operations []operationDetail `json:"operations"`
		}
		if err := call("list_operations", object{}, &list); err != nil {
			return err
		}
		settled := true
		for _, summary := range list.Operations {
			var op operationDetail
			if err := call("get_operation", object{"operation_id": summary.ID}, &op); err != nil {
				return err
			}
			for _, question := range op.Questions {
				if !question.Pending() {
					continue
				}
				settled = false
				if !op.Status.Terminal() {
					if err := call("answer_question", object{"operation_id": op.ID, "question_id": question.ID, "answer": "Test teardown: no further work."}, nil); err != nil {
						return err
					}
				}
			}
			if !op.Status.Terminal() {
				if err := call("cancel_operation", object{"operation_id": op.ID}, nil); err != nil {
					return err
				}
				settled = false
			}
			for _, task := range op.Tasks {
				if task.Status == operations.TaskQueued || task.NeedsRecovery() {
					settled = false
				}
			}
		}
		var inventory struct {
			Agents []agents.Agent `json:"agents"`
		}
		if err := call("list_agents", object{}, &inventory); err != nil {
			return err
		}
		for _, agent := range inventory.Agents {
			if agent.Work != nil {
				settled = false
			}
		}
		if settled {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("unfinished test operations: %w", ctx.Err())
		case <-tick.C:
		}
	}
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
	if result.StructuredContent != nil {
		a.t.Fatalf("%s: result must contain JSON once, in content text only", name)
	}
	raw := []byte(resultText(result))
	switch name {
	case "start_operation", "get_operation", "resolve_continuation", "answer_question", "complete_operation", "fail_operation", "cancel_operation":
		var payload struct {
			Progress map[string]json.RawMessage `json:"progress"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			a.t.Fatal(err)
		}
		for _, field := range []string{"task_counts", "total_tasks", "finished_tasks", "pending_decisions", "pending_questions", "settled"} {
			if value := payload.Progress[field]; len(value) == 0 || string(value) == "null" {
				a.t.Fatalf("%s omitted progress.%s", name, field)
			}
		}
	}
	if out == nil {
		return
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
func (a *api) start(text string) operations.Operation {
	a.t.Helper()
	var op operations.Operation
	a.call("start_operation", object{"mission_name": "analyze-text", "input": object{"text": text}}, &op)
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
func (a *api) task(opID, action string, input object) operations.Task {
	a.t.Helper()
	var task operations.Task
	a.call("assign_task", object{"operation_id": opID, "action_name": action, "input": input}, &task)
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

func summaryInput() object {
	return object{"character_count": 4, "word_count": 1, "line_count": 1}
}

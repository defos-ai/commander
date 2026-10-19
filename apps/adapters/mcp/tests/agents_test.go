package mcp_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAgentsAndActions(t *testing.T) {
	a := connect(t)
	var list struct {
		Agents []agents.Agent `json:"agents"`
	}
	a.call("list_agents", object{}, &list)
	var catalog struct {
		Actions []actions.Action `json:"actions"`
	}
	a.call("list_actions", object{}, &catalog)
	expectedCatalog := map[string]bool{}
	var webID string
	for _, agent := range list.Agents {
		if agent.Stale {
			t.Fatalf("incomplete/stale agent: %+v", agent)
		}
		var names []string
		for _, action := range agent.Actions {
			names = append(names, action.Name)
			expectedCatalog[action.Name] = true
			if action.Status != actions.Ready {
				t.Fatalf("Compose must prepare dependencies: %s: %+v", agent.ID, action)
			}
		}
		slices.Sort(names)
		if !slices.Equal(names, expectedAgentActions(string(agent.Type))) {
			t.Fatalf("agent advertises the wrong capabilities: %+v", agent)
		}
		if agent.Type == "web-agent" {
			webID = agent.ID
		}
	}
	if len(catalog.Actions) != len(expectedCatalog) {
		t.Fatalf("catalog does not match registered capabilities: %+v", catalog)
	}
	for _, action := range catalog.Actions {
		if !expectedCatalog[action.Name] {
			t.Fatalf("catalog contains unadvertised action: %+v", action)
		}
	}
	var detail agents.Agent
	a.call("describe_agent", object{"agent_id": webID}, &detail)
	if detail.ID != webID || detail.Status != agents.AgentReady {
		t.Fatalf("agent query lost identity or readiness: %+v", detail)
	}
	var action actions.Action
	a.call("describe_action", object{"agent_id": detail.ID, "action_name": "check-seo"}, &action)
	if len(action.InputSchema) == 0 || len(action.OutputSchema) == 0 || action.Status != actions.Ready {
		t.Fatalf("missing schema or readiness: %+v", action)
	}
	for _, query := range []struct {
		name string
		args object
	}{
		{"list_agents", object{}},
		{"list_actions", object{}},
		{"describe_agent", object{"agent_id": webID}},
		{"describe_action", object{"agent_id": webID, "action_name": "check-seo"}},
	} {
		var raw json.RawMessage
		a.call(query.name, query.args, &raw)
		if strings.Contains(string(raw), `"required_permissions"`) || strings.Contains(string(raw), `"granted_permissions"`) {
			t.Fatalf("%s still publishes application action permissions: %s", query.name, raw)
		}
	}

	tools, err := a.session.ListTools(a.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		switch tool.Name {
		case "run_action", "discover_actions", "stop_agent":
			t.Errorf("agent administration is exposed to the model: %s", tool.Name)
		case "assign_task", "resolve_continuation":
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(schema), `"agent_id"`) {
				t.Errorf("%s allows the caller to select an agent: %s", tool.Name, schema)
			}
		}
	}
	for _, name := range []string{"run_action", "discover_actions", "stop_agent"} {
		invalid := openSession(t)
		_, err := invalid.session.CallTool(invalid.ctx, &mcp.CallToolParams{Name: name, Arguments: object{"agent_id": detail.ID}})
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unknown tool") {
			t.Errorf("%s should be unavailable, got %v", name, err)
		}
	}

	url := website(t)
	op := a.start(url)
	assertAgentSelectionRejected(t, "assign_task", object{"operation_id": op.ID, "agent_id": detail.ID, "action_name": "check-seo", "input": object{"url": url}})
	if len(a.get(op.ID).Tasks) != 0 {
		t.Fatal("rejected request created work")
	}
	assigned := a.task(op.ID, "check-seo", url)
	settled := a.wait(op.ID, allTasksSucceeded)
	task := settled.Tasks[0]
	if task.ID != assigned.ID || task.AgentID == "" || task.Result == nil || len(task.Result.Output) == 0 {
		t.Fatalf("tracked action lost its executor or result: %+v", task)
	}
	knownAgent := false
	for _, agent := range list.Agents {
		knownAgent = knownAgent || agent.ID == task.AgentID && agent.Type == "web-agent"
	}
	if !knownAgent {
		t.Fatalf("controller selected an unannounced or incapable agent: %s", task.AgentID)
	}
	var idle agents.Agent
	a.call("describe_agent", object{"agent_id": task.AgentID}, &idle)
	if idle.Status != agents.AgentReady || idle.Work != nil {
		t.Fatalf("completed action still reported as current work: %+v", idle)
	}
	var saved operations.Task
	a.call("get_task", object{"operation_id": op.ID, "task_id": task.ID}, &saved)
	if saved.AgentID != task.AgentID {
		t.Fatalf("task query lost executor: %+v", saved)
	}
	a.call("complete_operation", object{"operation_id": op.ID, "output": object{"url": url, "summary": "SEO reviewed", "findings": []object{}}}, nil)
}

func assertAgentSelectionRejected(t *testing.T, name string, args object) {
	t.Helper()
	invalid := openSession(t)
	_, err := invalid.session.CallTool(invalid.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err == nil || !strings.Contains(err.Error(), "agent_id") {
		t.Fatalf("%s should reject agent_id, got %v", name, err)
	}
}

package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	missionsmcp "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStopAgentReceiptDistinguishesAcceptanceFromSavedShutdown(t *testing.T) {
	var commands *fakes.AgentCommands
	a, inventory := fixtures.HostWithInventory(t, func(config *missionsmcp.Config) {
		commands = fakes.NewAgentCommands(config.AgentStore)
		config.AgentCommands = commands
	})
	announced := fixtures.AgentWithDependencies()
	announced.Status = agents.AgentWorking
	announced.Work = &agents.Work{ActionName: "inspect", CommandID: id.New()}
	if err := inventory.Upsert(a.Context, announced); err != nil {
		t.Fatal(err)
	}
	input := fixtures.Object{"selector": fixtures.Object{"id": announced.ID}}
	var raw json.RawMessage

	a.Call("stop_agent", input, &raw)

	var receipt struct {
		Agents  []agents.Agent `json:"agents"`
		Message string         `json:"message"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.Agents) != 1 || receipt.Agents[0].ID != announced.ID || receipt.Agents[0].Version != announced.Version || receipt.Agents[0].Status != agents.AgentStopRequested || receipt.Agents[0].Stale || !strings.Contains(receipt.Message, "confirm shutdown") {
		t.Fatalf("stop receipt lost identity or claimed shutdown: %s", raw)
	}
	var fields struct {
		Agents []map[string]json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields.Agents[0]) != 4 {
		t.Fatalf("stop receipt included details beyond ID, version, status and stale: %s", raw)
	}
	var pending agents.Agent
	a.Call("describe_agent", fixtures.Object{"agent_id": announced.ID}, &pending)
	if pending.Status != agents.AgentStopRequested || pending.StopCommandID == "" || pending.StopRequestedAt.IsZero() || pending.Work == nil || pending.Work.CommandID != announced.Work.CommandID {
		t.Fatalf("readback lost stop identity or draining work: %+v", pending)
	}
	announced.ObservedAt = announced.ObservedAt.Add(time.Nanosecond)
	if err := inventory.Upsert(a.Context, announced); err != nil {
		t.Fatal(err)
	}

	a.Call("stop_agent", input, nil)
	var retained agents.Agent
	a.Call("describe_agent", fixtures.Object{"agent_id": announced.ID}, &retained)

	if retained.Status != agents.AgentStopRequested || retained.StopCommandID != pending.StopCommandID || !retained.StopRequestedAt.Equal(pending.StopRequestedAt) || commands.Calls() != 2 {
		t.Fatalf("heartbeat or repeated stop changed saved identity: %+v", retained)
	}
	announced.Status, announced.Work = agents.AgentStopped, nil
	announced.ObservedAt = announced.ObservedAt.Add(time.Nanosecond)
	if err := inventory.Upsert(a.Context, announced); err != nil {
		t.Fatal(err)
	}

	a.Call("stop_agent", input, &receipt)

	if len(receipt.Agents) != 1 || receipt.Agents[0].Status != agents.AgentStopped || !strings.Contains(receipt.Message, "Shutdown is confirmed") {
		t.Fatalf("already stopped agent did not return confirmed state: %+v", receipt)
	}
}

func TestStopAgentSelectorPreservesPartialErrorsAndEmptyMatches(t *testing.T) {
	var commands *fakes.AgentCommands
	a, inventory := fixtures.HostWithInventory(t, func(config *missionsmcp.Config) {
		commands = fakes.NewAgentCommands(config.AgentStore)
		config.AgentCommands = commands
	})
	first := fixtures.AgentWithDependencies()
	second := first.Clone()
	second.ID = "browser-agent-00000002"
	other := first.Clone()
	other.ID, other.Type = "worker-agent-00000001", "worker-agent"
	otherHost := first.Clone()
	otherHost.ID, otherHost.Server.Hostname = "browser-agent-00000003", "remote-browser-host"
	for _, record := range []agents.Agent{first, second, other, otherHost} {
		if err := inventory.Upsert(a.Context, record); err != nil {
			t.Fatal(err)
		}
	}
	commands.FailNextTarget(second.ID, "agent_stop_uncertain", true)
	var receipt struct {
		Agents  []agents.Agent     `json:"agents"`
		Errors  []agents.StopError `json:"errors"`
		Message string             `json:"message"`
	}

	a.Call("stop_agent", fixtures.Object{"selector": fixtures.Object{"type": string(first.Type), "version": first.Version, "hostname": first.Server.Hostname}}, &receipt)

	if len(receipt.Agents) != 1 || receipt.Agents[0].ID != first.ID || receipt.Agents[0].Status != agents.AgentStopRequested || len(receipt.Errors) != 1 || receipt.Errors[0].ID != second.ID || receipt.Errors[0].Error != "agent_stop_uncertain" || !strings.Contains(receipt.Message, "errors") || strings.Contains(receipt.Message, "Shutdown is confirmed") {
		t.Fatalf("selector result discarded partial uncertainty or claimed all stopped: %+v", receipt)
	}
	var uncertain, excluded agents.Agent
	a.Call("describe_agent", fixtures.Object{"agent_id": second.ID}, &uncertain)
	a.Call("describe_agent", fixtures.Object{"agent_id": other.ID}, &excluded)
	if uncertain.Status != agents.AgentStopRequested || uncertain.StopCommandID == "" || excluded.Status != agents.AgentReady || commands.Calls() != 1 {
		t.Fatalf("partial result was retried, discarded or ignored selector intersection: uncertain=%+v excluded=%+v calls=%d", uncertain, excluded, commands.Calls())
	}
	var excludedHost agents.Agent
	a.Call("describe_agent", fixtures.Object{"agent_id": otherHost.ID}, &excludedHost)
	if excludedHost.Status != agents.AgentReady || excludedHost.StopCommandID != "" {
		t.Fatalf("hostname stop filter affected another execution host: %+v", excludedHost)
	}
	var empty struct {
		Agents  json.RawMessage    `json:"agents"`
		Errors  []agents.StopError `json:"errors"`
		Message string             `json:"message"`
	}

	a.Call("stop_agent", fixtures.Object{"selector": fixtures.Object{"id": first.ID, "type": string(other.Type)}}, &empty)

	if string(empty.Agents) != "[]" || len(empty.Errors) != 0 || !strings.Contains(empty.Message, "No agents matched") {
		t.Fatalf("empty intersection did not return an empty successful result: %+v", empty)
	}
}

func TestStopAgentRejectsInvalidSelectorsAndKeepsUncertainOutcomesReadable(t *testing.T) {
	var commands *fakes.AgentCommands
	a, inventory := fixtures.HostWithInventory(t, func(config *missionsmcp.Config) {
		commands = fakes.NewAgentCommands(config.AgentStore)
		config.AgentCommands = commands
	})
	announced := fixtures.AgentWithDependencies()
	if err := inventory.Upsert(a.Context, announced); err != nil {
		t.Fatal(err)
	}

	for _, input := range []fixtures.Object{
		{}, {"selector": nil}, {"selector": fixtures.Object{}},
		{"selector": fixtures.Object{"id": nil}},
		{"selector": fixtures.Object{"id": "browser"}},
		{"selector": fixtures.Object{"id": announced.ID, "version": nil}},
		{"selector": fixtures.Object{"id": announced.ID, "type": ""}},
		{"selector": fixtures.Object{"type": string(announced.Type), "version": " "}},
		{"selector": fixtures.Object{"hostname": nil}},
		{"selector": fixtures.Object{"hostname": ""}},
		{"selector": fixtures.Object{"hostname": "https://remote-host"}},
		{"selector": fixtures.Object{"id": announced.ID, "owner": "someone"}},
		{"selector": fixtures.Object{"id": announced.ID}, "version": announced.Version},
		{"agent_id": announced.ID},
	} {
		_, err := a.Session.CallTool(a.Context, &mcp.CallToolParams{Name: "stop_agent", Arguments: input})
		var invalid *jsonrpc.Error
		if !errors.As(err, &invalid) || invalid.Code != jsonrpc.CodeInvalidParams {
			t.Fatalf("malformed stop input reached tool execution: input=%+v error=%v", input, err)
		}
	}
	if commands.Calls() != 0 {
		t.Fatalf("invalid requests dispatched %d controller commands", commands.Calls())
	}
	commands.FailNextStopAfterSave(errors.Join(agents.ErrUnknownAgent, context.DeadlineExceeded, errors.New("synthetic-backend-detail")))
	input := fixtures.Object{"selector": fixtures.Object{"id": announced.ID}}

	result, err := a.Session.CallTool(a.Context, &mcp.CallToolParams{Name: "stop_agent", Arguments: input})

	if err != nil || !result.IsError || len(result.Content) != 1 {
		t.Fatalf("uncertain stop did not return a tool error: %+v, %v", result, err)
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(content.Text, "agent_stop_uncertain") || !strings.Contains(content.Text, "list_agents") || !strings.Contains(content.Text, "describe_agent") || strings.Contains(content.Text, "synthetic-backend-detail") || strings.Contains(content.Text, "agent_not_found") {
		t.Fatalf("uncertainty lost readback guidance or exposed backend details: %+v", result.Content)
	}
	var saved agents.Agent
	a.Call("describe_agent", fixtures.Object{"agent_id": announced.ID}, &saved)
	if commands.Calls() != 1 || saved.Status != agents.AgentStopRequested || saved.StopCommandID == "" {
		t.Fatalf("uncertain outcome was retried or discarded instead of read back: calls=%d agent=%+v", commands.Calls(), saved)
	}
	withoutCommands, _ := fixtures.HostWithInventory(t)

	withoutCommands.Fails("stop_agent", input, "agent_controls_unavailable")
	withoutCommands.Call("list_agents", fixtures.Object{}, nil)
}

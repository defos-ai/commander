package missionsmcp

import (
	"context"
	"errors"
	"time"

	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type agentStopReceipt struct {
	Agents  []agentStopSummary `json:"agents"`
	Errors  []agents.StopError `json:"errors,omitempty"`
	Message string             `json:"message"`
}

type agentStopSummary struct {
	ID      string             `json:"id"`
	Version string             `json:"version,omitempty"`
	Status  agents.AgentStatus `json:"status"`
	Stale   bool               `json:"stale"`
}

const agentStopUncertain = "agent_stop_uncertain: the stop outcome is unknown; inspect list_agents and describe_agent before submitting again; do not retry automatically"

func stopSelectorSchema() map[string]any {
	return map[string]any{
		"type": "object", "minProperties": 1, "additionalProperties": false,
		"properties": map[string]any{
			"id": stringProperty, "type": stringProperty,
			"version":  map[string]any{"type": "string", "minLength": 1, "description": "Exact reported agent version; stable/latest are compared literally, not resolved."},
			"hostname": map[string]any{"type": "string", "minLength": 1, "description": "Exact advertised server.hostname. A container or Kubernetes Pod hostname is not its physical host or node."},
		},
	}
}

func addAgentCommands(server *mcp.Server, config Config) {
	addTool(server, "stop_agent", "Request graceful stop for agents matching selector fields id, type, version and hostname. Supplied fields are exact AND filters; at least one is required. Hostname matches the agent's own advertised server.hostname, not its parent spawner or Kubernetes node. The controller captures matches once; future agents are unaffected. New work is blocked while accepted work drains. stop_requested is acceptance, not shutdown. Inspect per-agent errors and use list_agents/describe_agent after uncertainty; never retry automatically. Deployment resources/restart policies stay host-managed.", objectSchema(map[string]any{"selector": stopSelectorSchema()}, "selector"),
		func(ctx context.Context, request commands.StopAgentsCommand) (agentStopReceipt, error) {
			if config.AgentCommands == nil {
				return agentStopReceipt{}, errors.New("agent_controls_unavailable: agent stopping is not configured on this MCP host")
			}
			bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			result, err := config.AgentCommands.StopAgents(bounded, request.Selector)
			if err != nil || result.Validate() != nil {
				return agentStopReceipt{}, errors.New(agentStopUncertain)
			}
			receipt := agentStopReceipt{Agents: make([]agentStopSummary, 0, len(result.Agents)), Errors: result.Errors}
			pending := false
			now := time.Now().UTC()
			for _, record := range result.Agents {
				record = record.Snapshot(now)
				pending = pending || record.Status == agents.AgentStopRequested
				receipt.Agents = append(receipt.Agents, agentStopSummary{ID: record.ID, Version: record.Version, Status: record.Status, Stale: record.Stale})
			}
			switch {
			case len(receipt.Errors) != 0:
				receipt.Message = "Some selected agents have errors. Inspect each error and saved state with describe_agent; do not retry automatically."
			case len(receipt.Agents) == 0:
				receipt.Message = "No agents matched the selector. No stop was requested."
			case pending:
				receipt.Message = "Stop requested for selected agents; accepted work may still be running. Read describe_agent to confirm shutdown."
			default:
				receipt.Message = "Shutdown is confirmed for selected agents. Deployment resources and restart policies remain host-managed."
			}
			return receipt, nil
		})
}

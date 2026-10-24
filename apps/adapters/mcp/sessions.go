package missionsmcp

import (
	"context"
	"errors"

	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/queries"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sessionScopeRequest struct {
	ScopeID string `json:"scope_id"`
}

func (r sessionScopeRequest) Validate() error {
	if !id.Valid(r.ScopeID) {
		return errors.New("scope_id must be a canonical UUID")
	}
	return nil
}

func registerSessions(server *mcp.Server, config Config) {
	addTool(server, "list_runtime_sessions", "Read agent-owned runtime sessions within one scope. Lease loss is reported as lost. Does not create or reconnect a session.",
		`{"type":"object","additionalProperties":false,"required":["scope_id"],"properties":{"scope_id":{"type":"string","format":"uuid"}}}`,
		func(ctx context.Context, request sessionScopeRequest) (struct {
			Sessions []sessions.Session `json:"sessions"`
		}, error) {
			result := struct {
				Sessions []sessions.Session `json:"sessions"`
			}{Sessions: []sessions.Session{}}

			items, err := (queries.Sessions{Store: config.SessionStore, Agents: config.AgentStore}).List(ctx, request.ScopeID)
			if err != nil {
				return result, err
			}
			result.Sessions = items
			return result, nil
		})
	if controller, ok := config.Operations.(commands.SessionController); ok {
		addTool(server, "control_runtime_session", "Inspect, verify or close an existing runtime session, or explicitly abandon a lost session. Owner controls remain available while a task is running. Abandonment releases the scope without confirming closure or changing operation affinity.",
			`{"type":"object","additionalProperties":false,"required":["id","owner_agent_id","scope_id","method"],"properties":{"id":{"type":"string","format":"uuid"},"owner_agent_id":{"type":"string"},"scope_id":{"type":"string","format":"uuid"},"request_id":{"type":"string","format":"uuid"},"method":{"type":"string","enum":["inspect","verify","close","abandon"]}}}`,
			func(ctx context.Context, request sessions.Control) (sessions.Session, error) {
				return controller.ControlSession(ctx, request)
			})
	}
}

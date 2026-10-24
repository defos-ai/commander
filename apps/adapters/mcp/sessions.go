package missionsmcp

import (
	"context"
	"encoding/json"
	"errors"

	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/queries"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sessionQueryRequest struct {
	ScopeID      sessionFilter `json:"scope_id,omitempty"`
	OwnerAgentID sessionFilter `json:"owner_agent_id,omitempty"`
}

type sessionFilter string

func (s *sessionFilter) UnmarshalJSON(data []byte) error {
	var value *string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value == nil || *value == "" {
		return errors.New("session filters must be nonempty strings")
	}
	*s = sessionFilter(*value)
	return nil
}

func (r sessionQueryRequest) query() sessions.Query {
	return sessions.Query{ScopeID: string(r.ScopeID), OwnerAgentID: string(r.OwnerAgentID)}
}

func (r sessionQueryRequest) Validate() error {
	return r.query().Validate()
}

func registerSessions(server *mcp.Server, config Config) {
	addTool(server, "list_runtime_sessions", "Read current and retained agent-owned runtime sessions, optionally filtered by scope and exact owner. Filters intersect. Lease loss is reported as lost. Does not create or reconnect a session.",
		`{"type":"object","additionalProperties":false,"properties":{"scope_id":{"type":"string","format":"uuid","minLength":1},"owner_agent_id":{"type":"string","minLength":1}}}`,
		func(ctx context.Context, request sessionQueryRequest) (struct {
			Sessions []sessions.Session `json:"sessions"`
		}, error) {
			result := struct {
				Sessions []sessions.Session `json:"sessions"`
			}{Sessions: []sessions.Session{}}

			items, err := (queries.Sessions{Store: config.SessionStore, Agents: config.AgentStore}).List(ctx, request.query())
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

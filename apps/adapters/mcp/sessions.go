package missionsmcp

import (
	"context"
	"errors"
	"time"

	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
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
			records, err := config.AgentStore.List(ctx)
			if err != nil {
				return result, err
			}
			for _, record := range records {
				for _, item := range record.Snapshot(time.Now().UTC()).Sessions {
					if item.ScopeID == request.ScopeID {
						result.Sessions = append(result.Sessions, item.Clone())
					}
				}
			}
			return result, nil
		})
	if controller, ok := config.Operations.(commands.SessionController); ok {
		addTool(server, "control_runtime_session", "Inspect, verify or close an existing runtime session through its owning agent. Does not create a session or run a user command. Closing remains available while a task is running.",
			`{"type":"object","additionalProperties":false,"required":["id","owner_agent_id","scope_id","method"],"properties":{"id":{"type":"string","format":"uuid"},"owner_agent_id":{"type":"string"},"scope_id":{"type":"string","format":"uuid"},"method":{"type":"string","enum":["inspect","verify","close"]}}}`,
			func(ctx context.Context, request sessions.Control) (sessions.Session, error) {
				return controller.ControlSession(ctx, request)
			})
	}
}

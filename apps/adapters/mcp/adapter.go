package missionsmcp

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/domain/sessions"
	frameworktesting "github.com/defos-ai/commander/framework/testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const artifactPrefix = "missions://artifacts/"

//go:embed assets/logo.png
var serverLogo []byte

type AgentRequest struct {
	AgentID string `json:"agent_id"`
}

func (r AgentRequest) Validate() error {
	if strings.TrimSpace(r.AgentID) == "" {
		return errors.New("agent_id must not be blank")
	}
	return nil
}

type ActionRequest struct {
	ActionName string `json:"action_name"`
	AgentID    string `json:"agent_id,omitempty"`
}

func (r ActionRequest) Validate() error {
	if strings.TrimSpace(r.ActionName) == "" {
		return errors.New("action_name must not be blank")
	}
	if r.AgentID != "" {
		return (AgentRequest{r.AgentID}).Validate()
	}
	return nil
}

type agentList struct {
	Agents []agentSummary `json:"agents"`
}

// Config supplies commands and stores independently. Mission creation writes
// the catalog; queries read saved state. Only operation commands require a
// running controller.
type Config struct {
	SessionStore      sessions.Store
	Operations        controllercommands.Operations
	AgentStore        agentmodel.AgentStore
	MissionStore      missions.MissionStore
	OperationStore    operations.OperationStore
	ReportStore       reports.ReportStore
	ArtifactStore     artifacts.ArtifactStore
	ArtifactFileStore artifacts.ArtifactFileStore
	ActionStore       actionmodel.ActionStore
	Logger            *slog.Logger
	// CatalogClient is caller-owned. CatalogURL is deployment configuration,
	// never tool input; an empty value selects the official discovery catalog.
	CatalogClient *packages.Client
	CatalogURL    string
	// TestCleaner is exposed only when ENV=tests. Call between settled tests.
	TestCleaner frameworktesting.TestCleaner
}

func NewServer(config Config) (*mcp.Server, error) {
	if config.Operations == nil || config.AgentStore == nil ||
		config.MissionStore == nil || config.OperationStore == nil || config.ReportStore == nil ||
		config.ArtifactStore == nil || config.ArtifactFileStore == nil || config.ActionStore == nil {
		return nil, errors.New("MCP requires operation commands and inventory, mission, operation, report, artifact metadata, artifact file and action stores")
	}
	if config.CatalogClient == nil {
		return nil, errors.New("MCP requires an agent catalog client")
	}
	if config.CatalogURL == "" {
		config.CatalogURL = DefaultAgentCatalogURL
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name: "commander-mcp", Version: "0.2.0",
		Icons: []mcp.Icon{{
			Source:   "data:image/png;base64," + base64.StdEncoding.EncodeToString(serverLogo),
			MIMEType: "image/png",
		}},
	}, &mcp.ServerOptions{
		Logger:       config.Logger,
		Instructions: "Check list_agents and action readiness before planning work. Use list_runtime_sessions to discover existing runtime sessions; optionally filter by scope_id or owner_agent_id, and check state, disposition and busy status before reuse. Use get_agent_catalog to discover agents available to install; pass package and visibility unchanged to the installation workflow described by the relevant agent. Direct GitHub URLs and references outside the catalog are also supported; the spawner resolves and saves the exact release during installation. Read relevant agents' responsibilities with describe_agent and action schemas with describe_action; follow the mission and agent guidance when capabilities are missing. Find a mission with list_missions; read describe_mission and relevant action schemas before starting. Use create_mission when a new reusable definition is needed, then inspect it with describe_mission. Creation does not execute work or install agents. Use tracked operations; specify actions and input, the controller selects agents. Check preflight, follow mission rules, and resolve pending model continuations using current versions. Review failed-task recovery before ending the operation; ask before changing scope. ask_question is for active-operation decisions; general clarifications belong in chat. Only record real operator answers; expiry is not approval. Finish with complete_operation using output_schema; report limitations. Completion accepts the final report and does not certify that every task succeeded. Terminal operations accept no new work or continuation decisions; get_report reads the saved report. Late execution results and artifacts can still arrive without changing the final status or report. Use progress.settled to distinguish final status from known task outcomes; interrupted outcomes remain unknown. New work needs a new operation. Inspect saved state before retrying an ambiguous failure. When active preflight has no ready candidate and lists agent_type and missing_requirements of kind secret, direct the operator to Admin UI Secrets for that agent type. Never request secret values in chat, tool inputs or ask_question. Running tasks may also expose agent_type and missing_requirements from their live exact execution; use the same Admin Secrets handoff or Playground masked form. Task waits retain their execution deadline. Saving a secret is not readiness; check preflight or task requirements before assuming work can proceed. Read artifacts with resources/read.",
	})
	registerSessions(server, config)
	if config.Logger != nil {
		server.AddReceivingMiddleware(requestLogging(config.Logger))
	}
	if os.Getenv("ENV") == "tests" && config.TestCleaner != nil {
		addTool(server, "clean_up_state", "Clear settled test operations, reports and artifacts. Keeps catalogs, agents, delivery records, bus and host memory. Active work must finish first.", emptySchema,
			func(ctx context.Context, _ struct{}) (struct{}, error) {
				return struct{}{}, frameworktesting.CleanContext(ctx, config.TestCleaner)
			})
	}
	addTool(server, "list_actions", "List action names and descriptions. Use describe_action for schemas and list_agents for current availability.", emptySchema,
		func(ctx context.Context, _ struct{}) (struct {
			Actions []actionSummary `json:"actions"`
		}, error) {
			items, err := config.ActionStore.List(ctx)
			summaries := make([]actionSummary, 0, len(items))
			for _, item := range items {
				summaries = append(summaries, actionSummary{Name: item.Name, Description: item.Description, SessionMode: item.SessionMode})
			}
			return struct {
				Actions []actionSummary `json:"actions"`
			}{summaries}, err
		})
	addTool(server, "describe_action", "Read one action's full input/output schemas. With agent_id, returns that agent's advertised definition and readiness; otherwise returns catalog metadata.", actionSchema,
		func(ctx context.Context, request ActionRequest) (actionmodel.Action, error) {
			if request.AgentID == "" {
				return config.ActionStore.Get(ctx, request.ActionName)
			}
			agent, err := config.AgentStore.Get(ctx, request.AgentID)
			if err != nil {
				return actionmodel.Action{}, err
			}
			for _, action := range agent.Actions {
				if action.Name == request.ActionName {
					return action, nil
				}
			}
			return actionmodel.Action{}, fmt.Errorf("%w: %s on agent %s", actionmodel.ErrUnknownAction, request.ActionName, request.AgentID)
		})
	addTool(server, "list_agents", "List agent IDs, responsibilities, current work, hosts, freshness and action readiness without schemas. Expired leases are stale. Use describe_action for schemas or describe_agent for full details.", emptySchema,
		func(ctx context.Context, _ struct{}) (agentList, error) {
			agents, err := config.AgentStore.List(ctx)
			if err != nil {
				return agentList{}, err
			}
			now := time.Now().UTC()
			sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
			summaries := make([]agentSummary, 0, len(agents))
			for _, agent := range agents {
				summaries = append(summaries, summarizeAgent(agent.Snapshot(now)))
			}
			return agentList{Agents: summaries}, nil
		})
	addTool(server, "describe_agent", "Inspect one announced agent, its responsibilities and usage guidance, current work, freshness and complete action catalog.", agentSchema,
		func(ctx context.Context, request AgentRequest) (agentmodel.Agent, error) {
			record, err := config.AgentStore.Get(ctx, request.AgentID)
			if err != nil {
				return agentmodel.Agent{}, err
			}
			return record.Snapshot(time.Now().UTC()), nil
		})
	addCatalogTools(server, config)
	addArtifactResources(server, config)
	addOperationTools(server, config)
	return server, nil
}

func addTool[Input, Output any](server *mcp.Server, name, description, schema string, run func(context.Context, Input) (Output, error)) {
	server.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: json.RawMessage(schema)},
		func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			arguments := request.Params.Arguments
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			var input Input
			if err := domain.Decode(arguments, &input); err != nil {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error()}
			}
			output, err := run(ctx, input)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			encoded, err := json.Marshal(output)
			if err != nil {
				return nil, fmt.Errorf("encode %s result: %w", name, err)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
		})
}

const emptySchema = `{"type":"object","properties":{},"additionalProperties":false}`
const agentSchema = `{"type":"object","properties":{"agent_id":{"type":"string","minLength":1}},"required":["agent_id"],"additionalProperties":false}`
const actionSchema = `{"type":"object","properties":{"action_name":{"type":"string","minLength":1},"agent_id":{"type":"string","minLength":1}},"required":["action_name"],"additionalProperties":false}`

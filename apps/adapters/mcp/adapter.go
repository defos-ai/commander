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

	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
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

// Config supplies commands and stores independently. Queries read saved state;
// only commands require a running controller.
type Config struct {
	Operations        controllercommands.Operations
	AgentStore        agentmodel.AgentStore
	MissionStore      missions.MissionStore
	OperationStore    operations.OperationStore
	ReportStore       reports.ReportStore
	ArtifactStore     artifacts.ArtifactStore
	ArtifactFileStore artifacts.ArtifactFileStore
	ActionStore       actionmodel.ActionStore
	Logger            *slog.Logger
	// TestCleaner is exposed only when ENV=tests. Call between settled tests.
	TestCleaner frameworktesting.TestCleaner
}

const installAgentInstructions = `Default all-in-one and Compose deployments start with only the spawner agent. Check list_agents and action readiness before planning work. To install a missing agent, read describe_mission for install-agent and describe_action for spawn-agent, start a separate install-agent operation with {"package":"<pinned reference>"}, then use assign_task for spawn-agent with the same input. The controller dispatches the task; never bypass it or queue provisioning inside a mission blocked on the missing actions.
Known packages: web HTTP/HTML checks use github.com/defos-ai/commander-web-agent@v0.2.0; browser screenshots/rendering use github.com/defos-ai/commander-browser-agent@v0.2.0. For other agents, obtain an explicit pinned package from the operator; do not invent a reference or assume it is published. Install only the agents needed for the requested work. Package installation does not install host dependencies or grant managed secrets or Docker privileges.
Review the saved spawn result, resolve its model continuation with no follow-up actions, and complete the installation operation using its output schema. Recheck list_agents and the target mission's preflight before running work; registration alone does not mean every action is ready. Inspect an uncertain launch before retrying; a new task may create another instance.`

func NewServer(config Config) (*mcp.Server, error) {
	if config.Operations == nil || config.AgentStore == nil ||
		config.MissionStore == nil || config.OperationStore == nil || config.ReportStore == nil ||
		config.ArtifactStore == nil || config.ArtifactFileStore == nil || config.ActionStore == nil {
		return nil, errors.New("MCP requires operation commands and inventory, mission, operation, report, artifact metadata, artifact file and action stores")
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name: "commander-mcp", Version: "0.1.0",
		Icons: []mcp.Icon{{
			Source:   "data:image/png;base64," + base64.StdEncoding.EncodeToString(serverLogo),
			MIMEType: "image/png",
		}},
	}, &mcp.ServerOptions{
		Logger:       config.Logger,
		Instructions: "Find a mission with list_missions; read describe_mission and relevant action schemas before starting. Use tracked operations; specify actions and input, the controller selects agents. Check preflight, follow mission rules, and resolve pending model continuations using current versions. Review failed-task recovery before ending the operation; ask before changing scope. ask_question is for active-operation decisions; general clarifications belong in chat. Only record real operator answers; expiry is not approval. Finish with complete_operation using output_schema; report limitations. Finished operations are read-only: get_report reads reports; new work needs a new operation. Inspect saved state before retrying an ambiguous failure. When active preflight has no ready candidate and lists agent_type and missing_requirements of kind secret, direct the operator to Admin UI Secrets for that agent type. Never request secret values in chat, tool inputs or ask_question. Saving a secret is not readiness; check preflight before assuming work can dispatch. Read artifacts with resources/read.\n" + installAgentInstructions,
	})
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
				summaries = append(summaries, actionSummary{item.Name, item.Description})
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
	addTool(server, "list_agents", "List agent IDs, current work, hosts, freshness and action readiness without schemas. Expired leases are stale. Use describe_action for schemas or describe_agent for full details.", emptySchema,
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
	addTool(server, "describe_agent", "Inspect one announced agent, its current work, freshness and complete action catalog.", agentSchema,
		func(ctx context.Context, request AgentRequest) (agentmodel.Agent, error) {
			record, err := config.AgentStore.Get(ctx, request.AgentID)
			if err != nil {
				return agentmodel.Agent{}, err
			}
			return record.Snapshot(time.Now().UTC()), nil
		})
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

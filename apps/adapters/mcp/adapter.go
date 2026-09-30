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

	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	frameworktesting "commander/framework/testing"

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

type DiscoveryRequest struct {
	AgentID string                     `json:"agent_id"`
	Scope   *agentmodel.DiscoveryScope `json:"scope,omitempty"`
}

func (r DiscoveryRequest) Validate() error { return (AgentRequest{r.AgentID}).Validate() }

type RunActionRequest struct {
	AgentID    string          `json:"agent_id"`
	ActionName string          `json:"action_name"`
	Input      json.RawMessage `json:"input,omitempty"`
}

func (r RunActionRequest) Validate() error {
	if err := (AgentRequest{r.AgentID}).Validate(); err != nil {
		return err
	}
	return (actionmodel.ActionCall{ActionName: r.ActionName, Input: r.Input}).Validate()
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

func NewServer(controller controllercommands.Controller, config Config) (*mcp.Server, error) {
	if controller == nil {
		return nil, errors.New("controller is required")
	}
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
		Instructions: "Find a mission with list_missions; read describe_mission and relevant action schemas before starting. Use tracked operations for mission work. Check preflight, follow automation, and resolve pending model continuations using current versions. Review failed-task recovery before ending the operation; ask before changing scope. ask_question is for active-operation decisions; general clarifications belong in chat. Only record real operator answers; expiry is not approval. Finish with complete_operation using output_schema; report limitations. Finished operations are read-only: get_report reads reports; new work needs a new operation. Inspect saved state before retrying an ambiguous failure. Never request secrets in chat or tool inputs. Read artifacts with resources/read.",
	})
	if config.Logger != nil {
		server.AddReceivingMiddleware(requestLogging(config.Logger))
	}
	if os.Getenv("ENV") == "tests" && config.TestCleaner != nil {
		addTool(server, "clean_up_state", "Clear persisted test data between tests. Keeps missions, bus messages, subscriptions and host memory.", emptySchema,
			func(_ context.Context, _ struct{}) (struct{}, error) {
				return struct{}{}, config.TestCleaner.Clean()
			})
	}
	addTool(server, "list_actions", "List action names, descriptions and required permissions. Use describe_action for schemas and list_agents for current availability and grants.", emptySchema,
		func(ctx context.Context, _ struct{}) (struct {
			Actions []actionSummary `json:"actions"`
		}, error) {
			items, err := config.ActionStore.List(ctx)
			summaries := make([]actionSummary, 0, len(items))
			for _, item := range items {
				summaries = append(summaries, actionSummary{item.Name, item.Description, item.RequiredPermissions})
			}
			return struct {
				Actions []actionSummary `json:"actions"`
			}{summaries}, err
		})
	addTool(server, "describe_action", "Read one action's full input/output schemas and required permissions. With agent_id, returns that agent's advertised definition and readiness; otherwise returns catalog metadata.", actionSchema,
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
	addTool(server, "list_agents", "List agent IDs, hosts, freshness, grants and action readiness without schemas. Expired leases are stale. Use describe_action for schemas or describe_agent for full details.", emptySchema,
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
	addTool(server, "describe_agent", "Inspect one announced agent, its freshness and complete action catalog.", agentSchema,
		func(ctx context.Context, request AgentRequest) (agentmodel.Agent, error) {
			record, err := config.AgentStore.Get(ctx, request.AgentID)
			if err != nil {
				return agentmodel.Agent{}, err
			}
			return record.Snapshot(time.Now().UTC()), nil
		})
	addTool(server, "discover_actions", "Rediscover actions on a running agent using the optional discovery scope.", discoverySchema,
		func(ctx context.Context, request DiscoveryRequest) (agentmodel.Agent, error) {
			return controller.DiscoverActions(ctx, request.AgentID, request.Scope)
		})
	addTool(server, "run_action", "Run one explicitly selected action on a fresh, running agent. Use its advertised input_schema. Returns inline observations or artifact references; read linked artifacts separately.", runSchema,
		func(ctx context.Context, request RunActionRequest) (actionmodel.ActionOutput, error) {
			return controller.RunAction(ctx, request.AgentID, actionmodel.ActionCall{ActionName: request.ActionName, Input: request.Input})
		})
	addTool(server, "stop_agent", "Stop a running agent after its queued work finishes.", agentSchema,
		func(ctx context.Context, request AgentRequest) (agentmodel.Agent, error) {
			return controller.StopAgent(ctx, request.AgentID)
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
			result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
			if actionOutput, ok := any(output).(actionmodel.ActionOutput); ok {
				for _, ref := range actionOutput.Artifacts {
					result.Content = append(result.Content, artifactLinks(ref)...)
				}
			}
			return result, nil
		})
}

const emptySchema = `{"type":"object","properties":{},"additionalProperties":false}`
const agentSchema = `{"type":"object","properties":{"agent_id":{"type":"string","minLength":1}},"required":["agent_id"],"additionalProperties":false}`
const actionSchema = `{"type":"object","properties":{"action_name":{"type":"string","minLength":1},"agent_id":{"type":"string","minLength":1}},"required":["action_name"],"additionalProperties":false}`
const discoverySchema = `{"type":"object","properties":{"agent_id":{"type":"string","minLength":1},"scope":{"type":["object","null"],"properties":{"paths":{"type":["array","null"],"items":{"type":"string"}},"services":{"type":["array","null"],"items":{"type":"string"}}},"additionalProperties":false}},"required":["agent_id"],"additionalProperties":false}`
const runSchema = `{"type":"object","properties":{"agent_id":{"type":"string","minLength":1},"action_name":{"type":"string","minLength":1},"input":{"type":"object","description":"Input matching the selected action's advertised input_schema."}},"required":["agent_id","action_name"],"additionalProperties":false}`

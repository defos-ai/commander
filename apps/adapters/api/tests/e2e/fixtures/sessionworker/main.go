package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	sessionworkerfakes "github.com/defos-ai/commander/apps/adapters/api/tests/fakes/sessionworker"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	frameworkcomms "github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	rabbitcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
)

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var bus frameworkcomms.Bus
	var err error
	busURL := os.Getenv("BUS_URL")
	if strings.HasPrefix(busURL, "amqp") {
		bus, err = rabbitcomms.NewRabbitMQBus(ctx, busURL, rabbitcomms.Options{})
	} else {
		bus, err = fscomms.NewFsBus(busURL)
	}
	if err != nil {
		return err
	}
	defer bus.Close()
	if os.Getenv("FIXTURE_PARTITION_CHECK") == "true" {
		bus = sessionworkerfakes.WithPartition(bus, os.Getenv("FIXTURE_PEER"))
	}
	files, err := artifacts.NewArtifactFileFsStore(os.Getenv("STORAGE_URL"))
	if err != nil {
		return err
	}
	owner := os.Getenv("FIXTURE_AGENT_ID")
	resources := sessionworkerfakes.NewResources(ctx, os.Getenv("FIXTURE_PEER"), os.Getenv("FIXTURE_SESSION_STATE"))
	var executors []action.Executor
	creationActions := []string{"propose-connections", "connect-resource", "propose-work"}
	if os.Getenv("FIXTURE_POLICY_ACTIONS") == "true" {
		creationActions = append(creationActions, "connect-secondary")
	}
	for _, name := range creationActions {
		mode := ""
		if name == "connect-resource" || name == "connect-secondary" {
			mode = "create"
		}
		inputSchema := json.RawMessage(`{"type":"object","required":["session_scope"],"properties":{"session_scope":{"type":"string"}}}`)
		if name == "propose-work" {
			inputSchema = json.RawMessage(`{"type":"object","required":["action_name"],"properties":{"action_name":{"type":"string"}}}`)
		}
		executors = append(executors, sessionworkerfakes.NewAction(actions.Action{
			Name: name, Description: "E2E connection fixture", SessionMode: mode,
			InputSchema:  inputSchema,
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		}, owner, os.Getenv("FIXTURE_PEER"), resources))
	}
	type useInput struct {
		Scope string `json:"session_scope"`
		ID    string `json:"session_id"`
	}
	type useOutput struct {
		Owner        string `json:"owner"`
		ID           string `json:"session_id"`
		CreationTask string `json:"creation_task"`
		Isolated     bool   `json:"checkpoint_isolated"`
	}
	secrets := []string(nil)
	if os.Getenv("FIXTURE_USE_REQUIRES_SECRET") == "true" {
		secrets = []string{"SESSION_ACCESS"}
	}
	useActions := []string{"use-resource"}
	if os.Getenv("FIXTURE_POLICY_ACTIONS") == "true" {
		useActions = append(useActions, "use-secondary")
	}
	if os.Getenv("FIXTURE_OMIT_USE") == "true" {
		useActions = nil
	}
	for _, name := range useActions {
		use, err := action.NewFunc(action.FuncConfig[useInput, useOutput]{Action: actions.Action{Name: name, SessionMode: "use", RequiredSecrets: secrets}, Run: func(ctx context.Context, input useInput) (useOutput, error) {
			binding, ok := action.SessionFromContext(ctx)
			if !ok {
				return useOutput{}, errors.New("missing binding")
			}
			_, found, err := action.LoadCheckpoint(ctx, "session_setup_intent")
			if err != nil {
				return useOutput{}, err
			}
			return useOutput{Owner: owner, ID: binding.ID, CreationTask: binding.Creation.TaskID, Isolated: !found}, nil
		}})
		if err != nil {
			return err
		}
		executors = append(executors, use)
	}
	marker, err := action.NewFunc(action.FuncConfig[map[string]any, map[string]string]{Action: actions.Action{Name: "mark-owner"}, Run: func(ctx context.Context, _ map[string]any) (map[string]string, error) {
		return map[string]string{"owner": owner}, nil
	}})
	if err != nil {
		return err
	}
	executors = append(executors, marker)
	interval := 500 * time.Millisecond
	worker, err := agent.NewAgent(agent.AgentConfig{Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})), ID: owner, Type: "fixture", Actions: executors,
		Sessions: resources, ArtifactFileStore: files, HeartbeatInterval: &interval,
		Server: agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "session-e2e"}})
	if err != nil {
		return err
	}
	err = worker.Serve(ctx, bus, nil)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

package fixtures

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	builder "github.com/defos-ai/commander/apps/agents/builder"
	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/crypto"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/secrets"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
	fsmissions "github.com/defos-ai/commander/providers/fs/missions"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
	fsreports "github.com/defos-ai/commander/providers/fs/reports"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
	fssessions "github.com/defos-ai/commander/providers/fs/sessions"
)

type Workspaces struct {
	Context    context.Context
	Controller *controller.Controller
	config     controller.ControllerConfig
	bus        *fscomms.FsBus
	storage    string
}

type WorkspaceBuilder struct {
	ID        string
	Directory string
	Started   string
	Provider  sessions.Provider
}

func NewWorkspaces(t *testing.T) *Workspaces {
	return newWorkspaces(t, 25*time.Second)
}

func NewSuccessfulBuild(t *testing.T) *Workspaces {
	return newWorkspaces(t, 90*time.Second)
}

func newWorkspaces(t *testing.T, timeout time.Duration) *Workspaces {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	root := t.TempDir()
	database, storage := filepath.Join(root, "database"), filepath.Join(root, "storage")
	bus := workspaceValue(fscomms.NewFsBus(filepath.Join(root, "bus")))
	t.Cleanup(func() { _ = bus.Close() })
	cipher := workspaceValue(crypto.NewSecretCipher("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	config := controller.ControllerConfig{
		MissionStore:       workspaceValue(fsmissions.NewMissionFsStore(database)),
		OperationStore:     workspaceValue(fsoperations.NewOperationFsStore(database)),
		ReportStore:        workspaceValue(fsreports.NewReportFsStore(storage)),
		AgentStore:         workspaceValue(fsagents.NewAgentFsStore(database)),
		ActionStore:        workspaceValue(fsactions.NewActionFsStore(database)),
		ArtifactStore:      workspaceValue(fsartifacts.NewArtifactFsStore(database)),
		ExecutionStore:     workspaceValue(fsexecutions.NewExecutionFsStore(database)),
		SessionStore:       workspaceValue(fssessions.NewSessionFsStore(database)),
		SecretStore:        workspaceValue(fssecrets.NewSecretFsStore(database)),
		SecretRequestStore: workspaceValue(fssecrets.NewRequestFsStore(database)),
		SecretDecryptor:    cipher, TaskTimeout: timeout - 5*time.Second,
		HeartbeatTimeout: time.Hour, LeaseSweepInterval: 100 * time.Millisecond,
	}
	service := workspaceValue(secrets.NewService(config.SecretStore, cipher))
	_, err := service.Create(ctx, secretmodel.Key{Name: "OPENAI_API_KEY"}, []byte("workspace-model-key"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	mission := missions.Mission{Name: "workspace-lifecycle", Objectives: []string{"Build using an owned workspace."},
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	if err := config.MissionStore.Upsert(ctx, mission); err != nil {
		t.Fatal(err)
	}
	control := workspaceValue(controller.NewController(ctx, bus, config))
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := control.Close(shutdown); err != nil {
			t.Error(err)
		}
	})
	return &Workspaces{Context: ctx, Controller: control, config: config, bus: bus, storage: storage}
}

func workspaceValue[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func (s *Workspaces) Builder(t *testing.T, suffix, checkoutMode, buildMode string) WorkspaceBuilder {
	t.Helper()
	root := t.TempDir()
	source, _ := Template(t)
	started := filepath.Join(root, "started")
	tools := fakes.GitConfig{Source: source, Mode: checkoutMode, Started: started, BuildMode: buildMode, BuildStarted: started}
	if buildMode == "success" {
		generatedBuild(t, &tools)
	}
	scope := GitScope(t, tools)
	files := workspaceValue(fsartifacts.NewArtifactFileFsStore(s.storage))
	executors, provider, err := builder.NewActions(root, files, "")
	if err != nil {
		t.Fatal(err)
	}
	interval := 100 * time.Millisecond
	instance := workspaceValue(builder.NewAgent(agent.AgentConfig{ID: "builder-agent-" + suffix, Actions: executors[:2], Sessions: provider,
		ArtifactFileStore: files, Server: agent.ServerInfo{Hostname: "workspace-fixture", IPAddress: "127.0.0.1"},
		HeartbeatInterval: &interval, Secrets: &agent.SecretConfig{}}, root))
	ctx, cancel := context.WithCancel(s.Context)
	done := make(chan error, 1)
	go func() { done <- instance.Serve(ctx, s.bus, scope) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("builder did not stop")
		}
	})
	s.Await(t, func() bool {
		record, err := s.config.AgentStore.Get(s.Context, instance.ID())
		if err != nil || record.Status != "ready" {
			return false
		}
		for _, executor := range record.Actions {
			if executor.Status != "ready" {
				return false
			}
		}
		return len(record.Actions) == 2
	})
	return WorkspaceBuilder{ID: instance.ID(), Directory: root, Started: started, Provider: provider}
}

func (s *Workspaces) ArtifactFile(t *testing.T, artifact artifacts.Artifact, file artifacts.ArtifactFile) []byte {
	t.Helper()
	store := workspaceValue(fsartifacts.NewArtifactFileFsStore(s.storage))
	data, err := store.Get(s.Context, artifact.ID, file)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (s *Workspaces) Start(t *testing.T) string {
	t.Helper()
	operation, err := s.Controller.Operations().Start(s.Context, commands.StartOperationCommand{MissionName: "workspace-lifecycle", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return operation.ID
}

func (s *Workspaces) Assign(t *testing.T, operationID, name string, input any) operations.Task {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Controller.Operations().Assign(s.Context, commands.AssignTaskCommand{OperationID: operationID, ActionName: name, Input: data})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (s *Workspaces) Finished(t *testing.T, operationID, taskID string) operations.Task {
	t.Helper()
	var task operations.Task
	s.Await(t, func() bool {
		operation, err := s.config.OperationStore.Get(s.Context, operationID)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range operation.Tasks {
			if item.ID == taskID {
				task = item
				return item.Status == operations.TaskSucceeded || item.Status == operations.TaskFailed
			}
		}
		return false
	})
	return task
}

func WorkspaceBuildInput(checkout builderactions.CheckoutTemplateOutput) builderactions.BuildAgentInput {
	return builderactions.BuildAgentInput{SessionScope: checkout.SessionScope, SessionID: checkout.SessionID, Instructions: "Implement a local inspection action.",
		Actions: []actionmodel.Definition{{Name: "inspect", Description: "Inspect supplied input.", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}}
}

func (s *Workspaces) Checkout(t *testing.T, operationID, scope string) (operations.Task, builderactions.CheckoutTemplateOutput) {
	t.Helper()
	task := s.Assign(t, operationID, builderactions.CheckoutTemplateName, builderactions.CheckoutTemplateInput{SessionScope: scope})
	task = s.Finished(t, operationID, task.ID)
	if task.Status != operations.TaskSucceeded || task.Result == nil {
		t.Fatalf("checkout failed: %+v", task)
	}
	var output builderactions.CheckoutTemplateOutput
	if err := json.Unmarshal(task.Result.Output, &output); err != nil {
		t.Fatal(err)
	}
	return task, output
}

func (s *Workspaces) CloseSession(t *testing.T, reference sessions.Reference) sessions.Session {
	t.Helper()
	closed, err := s.Controller.ControlSession(s.Context, sessions.Control{Reference: reference, Method: "close", RequestID: id.New()})
	if err != nil {
		t.Fatal(err)
	}
	return closed
}

func (s *Workspaces) AwaitStarted(t *testing.T, builder WorkspaceBuilder) {
	t.Helper()
	s.Await(t, func() bool { _, err := os.Stat(builder.Started); return err == nil })
}

func (s *Workspaces) Await(t *testing.T, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-time.After(10 * time.Millisecond):
		case <-s.Context.Done():
			t.Fatalf("workspace flow did not settle: %v", s.Context.Err())
		}
	}
}

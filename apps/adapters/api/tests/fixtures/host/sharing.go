package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/controller/workers/runner"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/operations"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fscontrollers "github.com/defos-ai/commander/providers/fs/controllers"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
	fsmissions "github.com/defos-ai/commander/providers/fs/missions"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
	fsreports "github.com/defos-ai/commander/providers/fs/reports"
)

type SharingStack struct {
	Context    context.Context
	Bus        *fakes.SharingBus
	Artifacts  *fakes.SharingArtifactStore
	Producer   *fakes.SharingProducer
	Controller *controller.Controller
	analyzers  map[string]*fakes.SharingAnalyzer
	agents     []sharingAgent
	runner     *runner.Worker
	runnerDone chan error
	server     *httptest.Server
	client     *http.Client
	cancel     context.CancelFunc
	busPath    string
}

type sharingAgent struct {
	cancel context.CancelFunc
	done   chan error
	bus    *fscomms.FsBus
}

func SharingMission(name string, routes []models.MissionSharingPolicyRequest) models.CreateMissionRequest {
	return models.CreateMissionRequest{Name: name, Objectives: []string{"Analyze accepted vulnerability findings as they arrive."},
		Policies:        &models.MissionPoliciesRequest{Sharing: routes},
		RequiredActions: []string{fakes.FindingAction, fakes.AnalysisAction, fakes.EvidenceAction},
		InputSchema:     json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
}

func NewSharingStack(t *testing.T) *SharingStack {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	root := t.TempDir()
	database, storage := filepath.Join(root, "database"), filepath.Join(root, "storage")
	s := &SharingStack{Context: ctx, cancel: cancel, busPath: filepath.Join(root, "bus"),
		analyzers: make(map[string]*fakes.SharingAnalyzer), client: &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}}
	t.Cleanup(func() { s.close(t) })
	require := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	bus, err := fscomms.NewFsBus(s.busPath)
	require(err)
	s.Bus = fakes.NewSharingBus(bus)
	config := api.Config{Security: api.SecurityConfig{Username: "sharing", Password: "sharing-fixture-password", AllowedHosts: []string{"127.0.0.1"}}}
	config.MissionStore, err = fsmissions.NewMissionFsStore(database)
	require(err)
	config.OperationStore, err = fsoperations.NewOperationFsStore(database)
	require(err)
	config.ExecutionStore, err = fsexecutions.NewExecutionFsStore(database)
	require(err)
	config.AgentStore, err = fsagents.NewAgentFsStore(database)
	require(err)
	config.ActionStore, err = fsactions.NewActionFsStore(database)
	require(err)
	config.ArtifactStore, err = fsartifacts.NewArtifactFsStore(database)
	require(err)
	config.ArtifactFileStore, err = fsartifacts.NewArtifactFileFsStore(storage)
	require(err)
	config.ReportStore, err = fsreports.NewReportFsStore(storage)
	require(err)
	config.ControllerStore, err = fscontrollers.NewControllerFsStore(database)
	require(err)
	s.Artifacts = &fakes.SharingArtifactStore{ArtifactStore: config.ArtifactStore}
	s.Controller, err = controller.NewController(ctx, s.Bus, controller.ControllerConfig{
		ArtifactStore: s.Artifacts, ExecutionStore: config.ExecutionStore, AgentStore: config.AgentStore,
		ActionStore: config.ActionStore, OperationStore: config.OperationStore,
	})
	require(err)
	s.runner, err = runner.New(ctx, s.Controller, runner.Dependencies{MissionStore: config.MissionStore,
		OperationStore: config.OperationStore, ReportStore: config.ReportStore, AgentCatalog: s.Controller, TaskTimeout: 20 * time.Second})
	require(err)
	s.runnerDone = make(chan error, 1)
	go func() { s.runnerDone <- s.runner.Run(ctx) }()
	config.Operations = s.runner
	handler, err := api.NewHandler(config)
	require(err)
	s.server = httptest.NewServer(handler)
	s.Producer = fakes.NewSharingProducer()
	s.startAgent(t, "producer", "producer-agent-00000001", s.Producer, config.ArtifactFileStore)
	for _, entry := range []struct{ id, name string }{
		{"analyzer-agent-00000001", fakes.AnalysisAction}, {"analyzer-agent-00000002", fakes.AnalysisAction},
		{"analyzer-agent-00000003", fakes.EvidenceAction},
	} {
		executor := fakes.NewSharingAnalyzer(entry.name, config.ArtifactFileStore)
		s.analyzers[entry.id] = executor
		s.startAgent(t, "analyzer", entry.id, executor, config.ArtifactFileStore)
	}
	return s
}

func (s *SharingStack) startAgent(t *testing.T, kind, id string, executor action.Executor, files artifacts.ArtifactFileStore) {
	t.Helper()
	bus, err := fscomms.NewFsBus(s.busPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(s.Context)
	running := sharingAgent{cancel: cancel, done: make(chan error, 1), bus: bus}
	s.agents = append(s.agents, running)
	interval := time.Second
	runtime, err := agent.NewAgent(agent.AgentConfig{ID: id, Type: agents.AgentType(kind), Actions: []action.Executor{executor},
		ArtifactFileStore: files, HeartbeatInterval: &interval, Server: agent.ServerInfo{Hostname: "sharing-fixture", IPAddress: "127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { running.done <- runtime.Serve(ctx, bus, nil) }()
	s.await(t, func() bool {
		response := s.Request(t, http.MethodGet, "/api/v1/agents/"+id, nil)
		if response.Status != http.StatusOK {
			return false
		}
		var observed models.AgentResponse
		return json.Unmarshal([]byte(response.Body), &observed) == nil && observed.Status == "ready" && !observed.Stale
	})
}

func (s *SharingStack) Request(t *testing.T, method, path string, body any) Response {
	t.Helper()
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(s.Context, method, s.server.URL+path, input)
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("sharing", "sharing-fixture-password")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return Response{Status: response.StatusCode, Header: response.Header, Body: string(data)}
}

func (s *SharingStack) AwaitTask(t *testing.T, operationID, taskID, status string) models.TaskResponse {
	t.Helper()
	var task models.TaskResponse
	s.await(t, func() bool {
		response := s.Request(t, http.MethodGet, "/api/v1/operations/"+operationID+"/tasks/"+taskID, nil)
		if response.Status != http.StatusOK {
			t.Fatalf("task read: %d %s", response.Status, response.Body)
		}
		if err := json.Unmarshal([]byte(response.Body), &task); err != nil {
			t.Fatal(err)
		}
		if task.Status == "failed" || task.Status == "interrupted" {
			t.Fatalf("task failed before %s: %+v", status, task)
		}
		return task.Status == status
	})
	return task
}

func (s *SharingStack) AwaitWorking(t *testing.T, operationID, taskID string) models.TaskResponse {
	t.Helper()
	task := s.AwaitTask(t, operationID, taskID, "running")
	s.await(t, func() bool {
		response := s.Request(t, http.MethodGet, "/api/v1/agents/"+task.AgentID, nil)
		var observed models.AgentResponse
		if response.Status != http.StatusOK || json.Unmarshal([]byte(response.Body), &observed) != nil {
			return false
		}
		return observed.Status == "working" && !observed.Stale && observed.Work != nil && observed.Work.OperationID == operationID && observed.Work.TaskID == taskID && observed.Work.CommandID == task.CommandID
	})
	return task
}

func (s *SharingStack) Analyzer(agentID string) *fakes.SharingAnalyzer { return s.analyzers[agentID] }

func (s *SharingStack) AwaitQuestion(t *testing.T, operationID, key string) models.QuestionResponse {
	t.Helper()
	var found models.QuestionResponse
	s.await(t, func() bool {
		response := s.Request(t, http.MethodGet, "/api/v1/operations/"+operationID+"/questions", nil)
		if response.Status != http.StatusOK {
			t.Fatalf("questions read: %d %s", response.Status, response.Body)
		}
		var questions models.QuestionListResponse
		if err := json.Unmarshal([]byte(response.Body), &questions); err != nil {
			t.Fatal(err)
		}
		for _, question := range questions.Items {
			if question.Key == key && question.Status == "pending" {
				found = question
				return true
			}
		}
		return false
	})
	return found
}

func (s *SharingStack) AnswerAsModel(operationID, questionID, answer string) error {
	_, err := s.runner.Answer(s.Context, controllercommands.AnswerQuestionCommand{
		OperationID: operationID, QuestionID: questionID, Answer: answer, AnsweredBy: operations.AnswerModel,
	})
	return err
}

func (s *SharingStack) StopAnalyzers() {
	for _, executor := range s.analyzers {
		executor.Stop()
	}
}

func (s *SharingStack) await(t *testing.T, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-time.After(10 * time.Millisecond):
		case <-s.Context.Done():
			t.Fatalf("sharing fixture did not become ready: %v", s.Context.Err())
		}
	}
}

func (s *SharingStack) close(t *testing.T) {
	t.Helper()
	if s.Bus != nil {
		s.Bus.ReleaseGates()
	}
	if s.Artifacts != nil {
		s.Artifacts.ReleaseGate()
	}
	if s.server != nil {
		s.server.Close()
	}
	s.StopAnalyzers()
	for _, running := range s.agents {
		running.cancel()
	}
	for _, running := range s.agents {
		select {
		case err := <-running.done:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("sharing agent shutdown: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("sharing agent did not stop")
		}
		if err := running.bus.Close(); err != nil {
			t.Error(err)
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.runner != nil {
		closeErr := s.runner.Close(shutdown)
		if closeErr != nil {
			t.Error(closeErr)
		}
		select {
		case err := <-s.runnerDone:
			if err != nil && err != runner.ErrClosed && !errors.Is(closeErr, err) {
				t.Error(err)
			}
		case <-shutdown.Done():
			if !errors.Is(closeErr, shutdown.Err()) {
				t.Error(shutdown.Err())
			}
		}
	}
	if s.Controller != nil {
		if err := s.Controller.Close(shutdown); err != nil {
			t.Error(err)
		}
	}
	s.cancel()
	if s.Bus != nil {
		if err := s.Bus.Close(); err != nil {
			t.Error(err)
		}
	}
	s.client.CloseIdleConnections()
}

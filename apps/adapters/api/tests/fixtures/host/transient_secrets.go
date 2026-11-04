package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
	fsmissions "github.com/defos-ai/commander/providers/fs/missions"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
	fsreports "github.com/defos-ai/commander/providers/fs/reports"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
)

type TransientSecretStack struct {
	API        *API
	Bus        *fakes.TransientSecretBus
	Action     *fakes.TransientSecretAction
	Requests   secrets.RequestStore
	Secrets    secrets.SecretStore
	AgentID    string
	Context    context.Context
	database   string
	controller *controller.Controller
	agentBus   *fscomms.FsBus
	agentDone  chan error
	cancel     context.CancelFunc
}

func NewTransientSecretStack(t *testing.T) *TransientSecretStack {
	t.Helper()
	fixture := New(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", SecretTestKey)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	stack := &TransientSecretStack{Context: ctx, cancel: cancel, database: filepath.Join(fixture.Root, "database")}
	t.Cleanup(func() { stack.close(t) })
	require := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	busPath, storage := filepath.Join(fixture.Root, "bus"), filepath.Join(fixture.Root, "storage")
	bus, err := fscomms.NewFsBus(busPath)
	require(err)
	stack.Bus = fakes.NewTransientSecretBus(bus)
	config := controller.ControllerConfig{TaskTimeout: 20 * time.Second, LeaseSweepInterval: 30 * time.Millisecond}
	config.MissionStore, err = fsmissions.NewMissionFsStore(stack.database)
	require(err)
	config.OperationStore, err = fsoperations.NewOperationFsStore(stack.database)
	require(err)
	config.ExecutionStore, err = fsexecutions.NewExecutionFsStore(stack.database)
	require(err)
	config.AgentStore, err = fsagents.NewAgentFsStore(stack.database)
	require(err)
	config.ActionStore, err = fsactions.NewActionFsStore(stack.database)
	require(err)
	config.ArtifactStore, err = fsartifacts.NewArtifactFsStore(stack.database)
	require(err)
	config.ReportStore, err = fsreports.NewReportFsStore(storage)
	require(err)
	stack.Secrets, err = fssecrets.NewSecretFsStore(stack.database)
	require(err)
	stack.Requests, err = fssecrets.NewRequestFsStore(stack.database)
	require(err)
	config.SecretStore, config.SecretRequestStore = stack.Secrets, stack.Requests
	config.SecretDecryptor, err = crypto.NewSecretCipher(SecretTestKey)
	require(err)
	stack.controller, err = controller.NewController(ctx, stack.Bus, config)
	require(err)
	stack.API = Start(t, fixture.LocalArgs())
	stack.agentBus, err = fscomms.NewFsBus(busPath)
	require(err)
	files, err := fsartifacts.NewArtifactFileFsStore(storage)
	require(err)
	stack.Action = fakes.NewTransientSecretAction()
	heartbeat := 100 * time.Millisecond
	runtime, err := agent.NewAgent(agent.AgentConfig{
		ID: "verification-agent-00000001", Type: "verification", Actions: []action.Executor{stack.Action},
		ArtifactFileStore: files, HeartbeatInterval: &heartbeat, Secrets: &agent.SecretConfig{
			RequestTTL: 15 * time.Second, RefreshInterval: 5 * time.Second, RetryInterval: 30 * time.Millisecond,
		}, Server: agent.ServerInfo{Hostname: "transient-fixture", IPAddress: "127.0.0.1"},
	})
	require(err)
	stack.AgentID, stack.agentDone = runtime.ID(), make(chan error, 1)
	go func() { stack.agentDone <- runtime.Serve(ctx, stack.agentBus, nil) }()
	stack.Await(t, func() bool {
		response := stack.API.Request(t, http.MethodGet, "/api/v1/agents/"+stack.AgentID, Authenticate)
		var observed models.AgentResponse
		return response.Status == http.StatusOK && json.Unmarshal([]byte(response.Body), &observed) == nil && observed.Status == "ready" && !observed.Stale
	})
	return stack
}

func (s *TransientSecretStack) Await(t *testing.T, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-time.After(10 * time.Millisecond):
		case <-s.Context.Done():
			t.Fatal("transient secret fixture did not finish within its lifetime")
		}
	}
}

func (s *TransientSecretStack) RetainRequest(t *testing.T, kind secrets.SecretType, status secrets.RequestStatus) secrets.Request {
	t.Helper()
	now := time.Now().UTC()
	request := secrets.Request{ID: id.New(), Type: kind, AgentID: s.AgentID, AgentType: "verification-agent",
		Key: secrets.Key{Name: "UNRELATED_TOKEN"}, Status: secrets.Pending, RequestedAt: now, ExpiresAt: now.Add(time.Minute)}
	if kind == secrets.SecretTransient {
		request.OperationID, request.TaskID, request.CommandID = id.New(), id.New(), id.New()
	}
	if status == secrets.RequestExpired {
		request.RequestedAt, request.ExpiresAt = now.Add(-2*time.Minute), now.Add(-time.Minute)
	}
	if err := s.Requests.Create(s.Context, request); err != nil {
		t.Fatal(err)
	}
	var err error
	switch status {
	case secrets.Cancelled:
		err = s.Requests.Cancel(s.Context, request.ID, now)
	case secrets.RequestExpired:
		err = s.Requests.Expire(s.Context, request.ID, now)
	}
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func (s *TransientSecretStack) AssertNoStoredValue(t *testing.T) {
	t.Helper()
	if _, err := s.Secrets.Get(s.Context, secrets.Key{Name: "CODE"}); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatal("transient value entered managed reusable storage")
	}
	private := []byte(fakes.TransientSecretValue)
	encoded := []byte(base64.StdEncoding.EncodeToString(private))
	for _, name := range []string{"secret-requests.json", "secrets.json"} {
		data, err := os.ReadFile(filepath.Join(s.database, name))
		if os.IsNotExist(err) && name == "secrets.json" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, private) || bytes.Contains(data, encoded) {
			t.Fatal("transient value entered persisted database data")
		}
	}
}

func (s *TransientSecretStack) close(t *testing.T) {
	t.Helper()
	if s.API != nil {
		s.API.Stop(t)
	}
	s.cancel()
	if s.agentDone != nil {
		select {
		case err := <-s.agentDone:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Error("transient agent did not shut down cleanly")
			}
		case <-time.After(3 * time.Second):
			t.Error("transient agent did not stop")
		}
	}
	if s.controller != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.controller.Close(ctx); err != nil {
			t.Error("transient controller did not shut down cleanly")
		}
	}
	if s.Bus != nil {
		s.Bus.Clear()
		if err := s.Bus.Close(); err != nil {
			t.Error(err)
		}
	}
	if s.agentBus != nil {
		if err := s.agentBus.Close(); err != nil {
			t.Error(err)
		}
	}
}

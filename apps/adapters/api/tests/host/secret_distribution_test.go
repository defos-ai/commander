package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	adapterconsumer "github.com/defos-ai/commander/framework/controller/consumers/adapter"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/crypto"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
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

// The HTTP host, controller and agent share only FS-backed providers. They do
// not share a plaintext cache or a request store instance with the agent.
func TestSecretDistributionFromAPIToIndependentAgent(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", secretTestKey)
	server := startAPI(t, fixture.localArgs())
	database := filepath.Join(fixture.root, "database")
	config := controller.ControllerConfig{LeaseSweepInterval: 100 * time.Millisecond}
	var err error
	if config.AgentStore, err = fsagents.NewAgentFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.ActionStore, err = fsactions.NewActionFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.ArtifactStore, err = fsartifacts.NewArtifactFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.ExecutionStore, err = fsexecutions.NewExecutionFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.SecretStore, err = fssecrets.NewSecretFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.SecretRequestStore, err = fssecrets.NewRequestFsStore(database); err != nil {
		t.Fatal(err)
	}
	controllerBus := newSecretTestBus(t, fixture.root)
	deliveries := make(chan messages.Message, 256)
	observedBus := &secretObservingBus{Bus: controllerBus, delivered: deliveries}
	startController := func() *controller.Controller {
		t.Helper()
		runtime, err := controller.NewController(t.Context(), observedBus, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { closeSecretController(t, runtime) })
		return runtime
	}
	coordinator := startController()
	cipher, err := crypto.NewSecretCipher(secretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	files, err := fsartifacts.NewArtifactFileFsStore(filepath.Join(fixture.root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	type input struct {
		Version int `json:"version"`
	}
	type output struct {
		Matched bool `json:"matched"`
	}
	private, err := action.NewFunc(action.FuncConfig[input, output]{
		Action: actionmodel.Action{Name: "use-secret", RequiredSecrets: []string{"GITHUB_TOKEN"}},
		Run: func(ctx context.Context, in input) (output, error) {
			value, err := action.Secret(ctx, "GITHUB_TOKEN")
			defer clear(value)
			if err != nil {
				return output{}, err
			}
			want := secretTestValue
			if in.Version == 2 {
				want = secretTestReplacement
			}
			return output{Matched: bytes.Equal(value, []byte(want))}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	public, err := action.NewFunc(action.FuncConfig[struct{}, output]{
		Action: actionmodel.Action{Name: "public"},
		Run: func(ctx context.Context, _ struct{}) (output, error) {
			value, err := action.Secret(ctx, "GITHUB_TOKEN")
			defer clear(value)
			return output{Matched: err != nil && len(value) == 0}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	onDemand, err := action.NewFunc(action.FuncConfig[struct{}, output]{
		Action: actionmodel.Action{Name: "on-demand"},
		Run: func(ctx context.Context, _ struct{}) (output, error) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			value, err := action.RequestSecret(ctx, "RUNTIME_TOKEN")
			defer clear(value)
			return output{Matched: bytes.Equal(value, []byte(secretTestValue))}, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Heartbeats only rediscover the agent after controller restart. Keep them
	// slower than secret retries so durable FS writes do not swamp that exchange.
	heartbeat := time.Second
	worker, err := agent.NewAgent(agent.AgentConfig{
		Type: "builder-agent", Actions: []action.Executor{private, public, onDemand}, ArtifactFileStore: files,
		HeartbeatInterval: &heartbeat,
		Secrets: &agent.SecretConfig{Decryptor: cipher, RequestTTL: 2 * time.Second,
			RefreshInterval: 600 * time.Millisecond, RetryInterval: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	agentBus := newSecretTestBus(t, fixture.root)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Serve(ctx, agentBus, nil) }()
	stopped := false
	stopAgent := func() {
		t.Helper()
		if stopped {
			return
		}
		cancel()
		select {
		case err := <-done:
			stopped = true
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("agent shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent shutdown timed out")
		}
	}
	t.Cleanup(stopAgent)
	awaitSecretCondition(t, "missing secret request and unrelated ready action", func() bool {
		pending, err := config.SecretRequestStore.ListPending(t.Context())
		return err == nil && len(pending) > 0 && secretActionStatus(worker, "use-secret") == action.Unavailable &&
			secretActionStatus(worker, "public") == action.Ready
	})

	// Resume pending synchronization from the saved database after a controller
	// restart, without restarting the agent or supplying a value through its env.
	closeSecretController(t, coordinator)
	coordinator = startController()
	stopOperations := startOperationRunner(t, coordinator, observedBus, database,
		missions.Mission{
			Name: "secret-distribution", Objective: "Use an agent's managed secret",
			RequiredActions: []string{"use-secret"}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
		},
		missions.Mission{
			Name: "on-demand-secret", Objective: "Request a secret during execution",
			RequiredActions: []string{"on-demand"}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
		},
	)
	operation := decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodPost, "/api/v1/operations",
		`{"mission_name":"secret-distribution","input":{}}`), http.StatusCreated)
	if operation.Status != "waiting" || operation.Preflight == nil || operation.Preflight.Ready || len(operation.Preflight.Actions) != 1 || len(operation.Preflight.Actions[0].UnavailableAgents) != 1 {
		t.Fatal("operation did not wait for its required action's managed secret")
	}
	blockedAgent := operation.Preflight.Actions[0].UnavailableAgents[0]
	if blockedAgent.AgentID != worker.ID() || blockedAgent.AgentType != "builder-agent" ||
		!slices.Equal(blockedAgent.MissingRequirements, []models.ActionRequirementResponse{{Kind: "secret", Name: "GITHUB_TOKEN"}}) {
		t.Fatal("public preflight did not identify the waiting agent type and managed secret")
	}
	operationPath := "/api/v1/operations/" + operation.ID
	queued := decodeResponse[models.TaskResponse](t, secretRequest(t, server, http.MethodPost, operationPath+"/tasks",
		`{"action_name":"use-secret","input":{"version":1}}`), http.StatusCreated)
	if queued.Status != "queued" || queued.AgentID != "" {
		t.Fatal("task dispatched before its secret was available")
	}
	waiting := decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodGet, operationPath, ""))
	if waiting.Status != "waiting" || len(waiting.Tasks) != 1 || waiting.Tasks[0].ID != queued.ID || waiting.Tasks[0].Status != "queued" {
		t.Fatal("API did not retain the accepted task while waiting for its secret")
	}
	created := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, secretsPath,
		`{"agent_type":"builder-agent","name":"GITHUB_TOKEN","value":"`+secretTestValue+`"}`), http.StatusCreated)
	awaitSecretInstallation(t, config.SecretRequestStore, deliveries, created.ID, 1)
	var resumed models.OperationResponse
	awaitSecretCondition(t, "queued operation task resuming after secret installation", func() bool {
		resumed = decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodGet, operationPath, ""))
		return len(resumed.Tasks) == 1 && resumed.Tasks[0].Status == "succeeded"
	})
	task := resumed.Tasks[0]
	var result output
	if resumed.Preflight == nil || !resumed.Preflight.Ready || len(resumed.Preflight.Actions) != 1 || len(resumed.Preflight.Actions[0].UnavailableAgents) != 0 ||
		task.ID != queued.ID || task.CommandID != queued.CommandID || task.AgentID != worker.ID() || task.Result == nil ||
		json.Unmarshal(task.Result.Output, &result) != nil || !result.Matched {
		t.Fatal("secret installation did not resume the original task with correct scoped access")
	}
	cancelled := decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodPost, operationPath+"/cancel", ""))
	if cancelled.Status != "cancelled" || len(cancelled.Tasks) != 1 || cancelled.Tasks[0].Status != "succeeded" {
		t.Fatal("operation cancellation did not retain the finished secret-backed task")
	}
	run := func(name string, version int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		body := json.RawMessage(`{}`)
		if name == "use-secret" {
			body = json.RawMessage(fmt.Sprintf(`{"version":%d}`, version))
		}
		result, err := coordinator.RunAction(ctx, worker.ID(), actionmodel.ActionCall{ActionName: name, Input: body})
		if err != nil {
			t.Fatalf("execute %s through controller: %v", name, err)
		}
		var decoded output
		if json.Unmarshal(result.Output, &decoded) != nil || !decoded.Matched {
			t.Fatal("action did not receive the expected scoped secret access")
		}
	}
	run("public", 0)

	replace := fmt.Sprintf(`{"id":%q,"version":1,"value":%q}`, created.ID, secretTestReplacement)
	decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPut, secretPath, replace), http.StatusOK)
	awaitSecretInstallation(t, config.SecretRequestStore, deliveries, created.ID, 2)
	run("use-secret", 2)
	deleted := secretRequest(t, server, http.MethodDelete, secretPath+"?id="+created.ID+"&version=2", "")
	if deleted.status != http.StatusNoContent {
		t.Fatal("secret deletion failed")
	}
	awaitSecretCondition(t, "deleted secret lease expiration", func() bool {
		return secretActionStatus(worker, "use-secret") == action.Unavailable
	})
	blocked, endAttempt := context.WithTimeout(t.Context(), 5*time.Second)
	_, err = coordinator.RunAction(blocked, worker.ID(), actionmodel.ActionCall{ActionName: "use-secret", Input: json.RawMessage(`{"version":2}`)})
	endAttempt()
	if err == nil {
		t.Fatal("new action execution used a deleted secret after its lease expired")
	}
	run("public", 0)

	// The action decides at run time that it needs a secret. It has no static
	// requirement, and uses the same API, saved request and encrypted delivery.
	pending, err := config.SecretRequestStore.ListPending(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range pending {
		if request.Request.Key.Name == "RUNTIME_TOKEN" {
			t.Fatal("on-demand secret was requested before the action asked for it")
		}
	}
	runtimeOperation := decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodPost, "/api/v1/operations",
		`{"mission_name":"on-demand-secret","input":{}}`), http.StatusCreated)
	runtimePath := "/api/v1/operations/" + runtimeOperation.ID
	runtimeTask := decodeResponse[models.TaskResponse](t, secretRequest(t, server, http.MethodPost, runtimePath+"/tasks",
		`{"action_name":"on-demand","input":{}}`), http.StatusCreated)
	awaitSecretCondition(t, "on-demand request visible through API", func() bool {
		for _, request := range listSecretRequests(t, server, "") {
			if request.AgentID == worker.ID() && request.Name == "RUNTIME_TOKEN" && request.Status == "needs_value" {
				return true
			}
		}
		return false
	})
	var runtimeWaiting models.OperationResponse
	awaitSecretCondition(t, "execution-scoped requirements visible through API", func() bool {
		runtimeWaiting = decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodGet, runtimePath, ""))
		return len(runtimeWaiting.Tasks) == 1 && len(runtimeWaiting.Tasks[0].MissingRequirements) == 1
	})
	waitingTask := runtimeWaiting.Tasks[0]
	if waitingTask.Status != "running" || waitingTask.ID != runtimeTask.ID || waitingTask.CommandID != runtimeTask.CommandID ||
		waitingTask.AgentType != "builder-agent" || !slices.Equal(waitingTask.MissingRequirements, []models.ActionRequirementResponse{{Kind: "secret", Name: "RUNTIME_TOKEN"}}) ||
		runtimeWaiting.Preflight == nil || !runtimeWaiting.Preflight.Ready {
		t.Fatal("runtime wait must affect the assigned execution, not action preflight")
	}
	detail := decodeResponse[models.TaskResponse](t, secretRequest(t, server, http.MethodGet, runtimePath+"/tasks/"+runtimeTask.ID, ""))
	if !slices.Equal(detail.MissingRequirements, waitingTask.MissingRequirements) || detail.AgentType != waitingTask.AgentType {
		t.Fatal("task detail lost execution requirements")
	}
	// Another operation on the same agent type must not inherit this wait.
	previous := decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodGet, operationPath, ""))
	if len(previous.Tasks[0].MissingRequirements) != 0 {
		t.Fatal("runtime wait leaked into another operation")
	}
	decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, secretsPath,
		`{"agent_type":"builder-agent","name":"RUNTIME_TOKEN","value":"`+secretTestValue+`"}`), http.StatusCreated)
	var runtimeResumed models.OperationResponse
	awaitSecretCondition(t, "same execution resumes after on-demand secret submission", func() bool {
		runtimeResumed = decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodGet, runtimePath, ""))
		return len(runtimeResumed.Tasks) == 1 && runtimeResumed.Tasks[0].Status == "succeeded"
	})
	completed := runtimeResumed.Tasks[0]
	if completed.ID != runtimeTask.ID || completed.CommandID != runtimeTask.CommandID || completed.Result == nil ||
		json.Unmarshal(completed.Result.Output, &result) != nil || !result.Matched || len(completed.MissingRequirements) != 0 {
		t.Fatal("on-demand execution did not finish with cleared requirements")
	}
	decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodPost, runtimePath+"/cancel", ""))
	run("on-demand", 0)
	stopAgent()
	stopOperations()
	closeSecretController(t, coordinator)
	server.stop(t)
	assertNoDistributedPlaintext(t, fixture.root)
}

func startOperationRunner(t *testing.T, coordinator *controller.Controller, bus comms.Bus, database string, definitions ...missions.Mission) func() {
	t.Helper()
	missionStore, err := fsmissions.NewMissionFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if err := missionStore.Upsert(t.Context(), definition); err != nil {
			t.Fatal(err)
		}
	}
	operationStore, err := fsoperations.NewOperationFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	reportStore, err := fsreports.NewReportFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := runner.NewRunner(t.Context(), coordinator, runner.Config{
		MissionStore: missionStore, OperationStore: operationStore, ReportStore: reportStore, AgentCatalog: coordinator,
	})
	if err != nil {
		t.Fatal(err)
	}
	commandCtx, cancelCommands := context.WithCancel(t.Context())
	var commands *adapterconsumer.Consumer
	var done chan error
	stopped := false
	stop := func() {
		t.Helper()
		if stopped {
			return
		}
		stopped = true
		// Stop the reader before closing subscriptions, including in-flight ACKs.
		cancelCommands()
		if commands != nil {
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("operation command consumer: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("operation command consumer shutdown timed out")
			}
			if err := commands.Close(); err != nil {
				t.Errorf("operation command consumer shutdown: %v", err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := execution.Close(ctx); err != nil {
			t.Errorf("operation runner shutdown: %v", err)
		}
	}
	t.Cleanup(stop)
	commands, err = adapterconsumer.NewConsumer(t.Context(), bus, messages.DefaultControllerType, coordinator.ID(), coordinator, execution, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	done = make(chan error, 1)
	go func() { done <- commands.Consume(commandCtx) }()
	return stop
}

type secretObservingBus struct {
	comms.Bus
	delivered chan messages.Message
}

func (bus *secretObservingBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if err := bus.Bus.Publish(ctx, topic, message); err != nil {
		return err
	}
	if message.Type == agentevents.SecretDelivered {
		select {
		case bus.delivered <- message:
		default:
		}
	}
	return nil
}

func newSecretTestBus(t *testing.T, root string) *fscomms.FsBus {
	t.Helper()
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	return bus
}

func closeSecretController(t *testing.T, runtime *controller.Controller) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Close(ctx); err != nil {
		t.Errorf("controller shutdown: %v", err)
	}
}

func secretActionStatus(worker *agent.Agent, name string) action.Status {
	for _, definition := range worker.Describe().Actions {
		if definition.Name == name {
			return definition.Status
		}
	}
	return action.Unknown
}

func awaitSecretCondition(t *testing.T, description string, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", description)
		case <-ticker.C:
		}
	}
}

func awaitSecretInstallation(t *testing.T, requests secretmodel.RequestStore, deliveries <-chan messages.Message, secretID string, version uint64) {
	t.Helper()
	var requestID string
	awaitSecretCondition(t, "matching installed secret version", func() bool {
		for {
			select {
			case message := <-deliveries:
				payload, err := agentevents.DecodeSecretDelivery(message)
				if err == nil && payload.Secret.Metadata.ID == secretID && payload.Secret.Metadata.Version == version {
					requestID = message.CorrelationID
				}
			default:
				if requestID == "" {
					return false
				}
				record, err := requests.Get(t.Context(), requestID)
				return err == nil && record.Request.Status == secretmodel.Fulfilled
			}
		}
	})
}

func assertNoDistributedPlaintext(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, private := range []string{secretTestKey, secretTestValue, secretTestReplacement} {
			if bytes.Contains(data, []byte(private)) {
				t.Error("secret synchronization persisted plaintext or key material")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

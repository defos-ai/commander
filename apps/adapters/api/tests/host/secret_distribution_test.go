package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	fakes "github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/crypto"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
)

// The HTTP host, controller and agent share only FS-backed providers. They do
// not share a plaintext cache or a request store instance with the agent.
func TestSecretDistributionFromAPIToIndependentAgent(t *testing.T) {
	fixture := hostfixtures.New(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", hostfixtures.SecretTestKey)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	database := filepath.Join(fixture.Root, "database")
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
	cipher, err := crypto.NewSecretCipher(hostfixtures.SecretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	config.SecretDecryptor = cipher
	controllerBus := hostfixtures.NewSecretTestBus(t, fixture.Root)
	observedBus := fakes.NewSecretObservingBus(controllerBus)
	deliveries := observedBus.Deliveries()
	startController := func() *controller.Controller {
		t.Helper()
		runtime, err := controller.NewController(t.Context(), observedBus, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { hostfixtures.CloseSecretController(t, runtime) })
		return runtime
	}
	coordinator := startController()
	files, err := fsartifacts.NewArtifactFileFsStore(filepath.Join(fixture.Root, "storage"))
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
			want := hostfixtures.SecretTestValue
			if in.Version == 2 {
				want = hostfixtures.SecretTestReplacement
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
			return output{Matched: bytes.Equal(value, []byte(hostfixtures.SecretTestValue))}, err
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
		Secrets: &agent.SecretConfig{RequestTTL: 2 * time.Second,
			RefreshInterval: 600 * time.Millisecond, RetryInterval: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	agentBus := hostfixtures.NewSecretTestBus(t, fixture.Root)
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
	hostfixtures.AwaitSecretCondition(t, "missing secret request and unrelated ready action", func() bool {
		pending, err := config.SecretRequestStore.ListPending(t.Context())
		return err == nil && len(pending) > 0 && hostfixtures.SecretActionStatus(worker, "use-secret") == action.Unavailable &&
			hostfixtures.SecretActionStatus(worker, "public") == action.Ready
	})

	// Resume pending synchronization from the saved database after a controller
	// restart, without restarting the agent or supplying a value through its env.
	hostfixtures.CloseSecretController(t, coordinator)
	coordinator = startController()
	stopOperations := hostfixtures.StartOperationRunner(t, coordinator, observedBus, database,
		missions.Mission{
			Name: "secret-distribution", Objectives: []string{"Use an agent's managed secret"},
			RequiredActions: []string{"use-secret"}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
		},
		missions.Mission{
			Name: "on-demand-secret", Objectives: []string{"Request a secret during execution"},
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
	created := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, hostfixtures.SecretsPath,
		`{"name":"GITHUB_TOKEN","value":"`+hostfixtures.SecretTestValue+`"}`), http.StatusCreated)
	hostfixtures.AwaitSecretInstallation(t, config.SecretRequestStore, deliveries, created.ID, 1)
	var resumed models.OperationResponse
	hostfixtures.AwaitSecretCondition(t, "queued operation task resuming after secret installation", func() bool {
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

	replace := fmt.Sprintf(`{"id":%q,"version":1,"value":%q}`, created.ID, hostfixtures.SecretTestReplacement)
	decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPut, hostfixtures.SecretPath, replace), http.StatusOK)
	hostfixtures.AwaitSecretInstallation(t, config.SecretRequestStore, deliveries, created.ID, 2)
	run("use-secret", 2)
	deleted := secretRequest(t, server, http.MethodDelete, hostfixtures.SecretPath+"?id="+created.ID+"&version=2", "")
	if deleted.Status != http.StatusNoContent {
		t.Fatal("secret deletion failed")
	}
	hostfixtures.AwaitSecretCondition(t, "deleted secret lease expiration", func() bool {
		return hostfixtures.SecretActionStatus(worker, "use-secret") == action.Unavailable
	})
	blocked, endAttempt := context.WithTimeout(t.Context(), 5*time.Second)
	_, err = coordinator.RunAction(blocked, worker.ID(), actionmodel.ActionCall{ActionName: "use-secret", Input: json.RawMessage(`{"version":2}`)})
	endAttempt()
	if err == nil {
		t.Fatal("new action execution used a deleted secret after its lease expired")
	}
	run("public", 0)

	// The action decides at run time that it needs a secret. It has no static
	// requirement, and uses the same API, saved request and scoped delivery.
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
	hostfixtures.AwaitSecretCondition(t, "on-demand request visible through API", func() bool {
		for _, request := range listSecretRequests(t, server, "") {
			if request.AgentID == worker.ID() && request.Name == "RUNTIME_TOKEN" && request.Status == "needs_value" {
				return true
			}
		}
		return false
	})
	var runtimeWaiting models.OperationResponse
	hostfixtures.AwaitSecretCondition(t, "execution-scoped requirements visible through API", func() bool {
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
	tasks := decodeResponse[models.PageResponse[models.TaskResponse]](t, secretRequest(t, server, http.MethodGet, runtimePath+"/tasks?limit=1", ""))
	if len(tasks.Items) != 1 || tasks.NextCursor != waitingTask.ID || tasks.Items[0].AgentType != waitingTask.AgentType ||
		!slices.Equal(tasks.Items[0].MissingRequirements, waitingTask.MissingRequirements) {
		t.Fatal("task page lost execution requirements or its cursor")
	}
	listed := decodeResponse[models.ListResponse[models.OperationResponse]](t, secretRequest(t, server, http.MethodGet, "/api/v1/operations", ""))
	index := slices.IndexFunc(listed.Items, func(item models.OperationResponse) bool { return item.ID == runtimeOperation.ID })
	if index < 0 || len(listed.Items[index].Tasks) != 1 || listed.Items[index].Tasks[0].AgentType != waitingTask.AgentType ||
		!slices.Equal(listed.Items[index].Tasks[0].MissingRequirements, waitingTask.MissingRequirements) {
		t.Fatal("operation list lost execution requirements")
	}
	// Another operation on the same agent type must not inherit this wait.
	previous := decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodGet, operationPath, ""))
	if len(previous.Tasks[0].MissingRequirements) != 0 {
		t.Fatal("runtime wait leaked into another operation")
	}
	decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, hostfixtures.SecretsPath,
		`{"name":"RUNTIME_TOKEN","value":"`+hostfixtures.SecretTestValue+`"}`), http.StatusCreated)
	var runtimeResumed models.OperationResponse
	hostfixtures.AwaitSecretCondition(t, "same execution resumes after on-demand secret submission", func() bool {
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
	hostfixtures.CloseSecretController(t, coordinator)
	server.Stop(t)
	assertNoPlaintextInSecretStores(t, fixture.Root)
}

func assertNoPlaintextInSecretStores(t *testing.T, root string) {
	t.Helper()
	for _, directory := range []string{"database", "storage"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, private := range []string{hostfixtures.SecretTestKey, hostfixtures.SecretTestValue, hostfixtures.SecretTestReplacement} {
				if bytes.Contains(data, []byte(private)) {
					t.Error("secret synchronization persisted plaintext or key material in its stores")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

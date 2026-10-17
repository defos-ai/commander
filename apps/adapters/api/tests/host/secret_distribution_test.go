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
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/crypto"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
)

// The HTTP host, controller and agent share only FS-backed providers. They do
// not share a plaintext cache or a request store instance with the agent.
func TestSecretDistributionFromAPIToIndependentAgent(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("SECRET_ENCRYPTION_KEY", secretTestKey)
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
	heartbeat := 100 * time.Millisecond
	worker, err := agent.NewAgent(agent.AgentConfig{
		Type: "builder-agent", Actions: []action.Executor{private, public}, ArtifactFileStore: files,
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
	created := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, secretsPath,
		`{"agent_type":"builder-agent","name":"GITHUB_TOKEN","value":"`+secretTestValue+`"}`), http.StatusCreated)
	awaitSecretInstallation(t, config.SecretRequestStore, deliveries, created.ID, 1)
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
	run("use-secret", 1)
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
	stopAgent()
	closeSecretController(t, coordinator)
	server.stop(t)
	assertNoDistributedPlaintext(t, fixture.root)
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

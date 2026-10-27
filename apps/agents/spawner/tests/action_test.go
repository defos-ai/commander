package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	spawner "github.com/defos-ai/commander/apps/agents/spawner"
	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
)

const actionPackage = "github.com/defos-ai/commander/apps/agents/example@v0.1.0"

func TestSpawnerAgentHasIndependentCatalogs(t *testing.T) {
	launcher := &actionLauncher{}
	config := agent.AgentConfig{Type: "ignored", ArtifactFileStore: actionArtifacts{},
		Server: agent.ServerInfo{Hostname: "fixture", IPAddress: "127.0.0.1"}}
	first, err := spawner.NewAgent(config, launcher)
	if err != nil {
		t.Fatal(err)
	}
	second, err := spawner.NewAgent(config, launcher)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() == second.ID() || !id.ValidAgent(first.ID()) || !strings.HasPrefix(first.ID(), "spawner-agent-") {
		t.Fatalf("spawner identity is not fresh: %s / %s", first.ID(), second.ID())
	}
	if err := first.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		instance *agent.Agent
		status   action.Status
	}{{first, action.Ready}, {second, action.Unknown}} {
		snapshot := test.instance.Describe()
		if snapshot.Type != spawner.AgentType || len(snapshot.Actions) != 1 ||
			snapshot.Actions[0].Name != spawneractions.SpawnAgentName || snapshot.Actions[0].Status != test.status {
			t.Fatalf("incorrect spawner catalog: %+v", snapshot)
		}
		if err := snapshot.Actions[0].Validate(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(snapshot.Responsibilities, spawner.Responsibilities()) {
			t.Fatal("published responsibilities differ from the registered spawner")
		}
		descriptions := spawner.ActionDescriptions()
		if len(descriptions) != len(snapshot.Actions) || descriptions[0].Name != snapshot.Actions[0].Name || descriptions[0].Description != snapshot.Actions[0].Description {
			t.Fatal("published action descriptions differ from the registered catalog")
		}
	}
	if len(launcher.calls) != 0 {
		t.Fatal("construction or discovery launched a process")
	}
	config.Actions = []action.Executor{}
	empty, err := spawner.NewAgent(config, nil)
	if err != nil || len(empty.Describe().Actions) != 0 {
		t.Fatalf("explicit empty catalog was replaced: %v", err)
	}
	config.Actions = nil
	if _, err := spawner.NewAgent(config, nil); err == nil {
		t.Fatal("default catalog accepted a missing launcher")
	}
	if names := spawner.ActionNames(); !reflect.DeepEqual(names, []string{spawneractions.SpawnAgentName}) {
		t.Fatalf("unexpected catalog names: %v", names)
	}
}

func TestSpawnAgentUsesExecutionIdentityAndReturnsReadiness(t *testing.T) {
	launcher := &actionLauncher{result: runtimes.Result{
		AgentID: "example-agent-12345678", AgentType: "example-agent", Package: actionPackage, Runtime: runtimes.RuntimeNative, RuntimeDetails: runtimes.NativeRuntimeDetails{PID: 42},
		Actions: []actionmodel.Action{{Name: "inspect", Description: "Inspect a local target", Status: action.Unavailable,
			MissingRequirements: []actionmodel.Requirement{{Kind: actionmodel.ExecutableRequirement, Name: "example-tool"}},
			InputSchema:         json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}},
	}}
	executor := newSpawnAction(t, launcher)
	work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID(), StepID: "provision"}
	for _, execution := range []action.Execution{{CommandID: id.MessageID(), Work: work}, {CommandID: id.MessageID()}} {
		for _, reference := range []string{actionPackage, "https://github.com/defos-ai/commander-example-agent", "github.com/defos-ai/commander-example-agent@stable"} {
			ctx := action.WithExecution(t.Context(), execution)
			result, err := executor.Run(ctx, json.RawMessage(`{"package":"`+reference+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			var actual runtimes.Result
			if err := json.Unmarshal(result.Output, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, launcher.result) {
				t.Fatalf("spawn output lost registration or action readiness: %+v", actual)
			}
			wantID := "command:" + execution.CommandID
			if call := launcher.calls[len(launcher.calls)-1]; call != (spawnInvocation{requestID: wantID, reference: reference}) {
				t.Fatalf("launcher received wrong dispatch identity: %+v", call)
			}
		}
	}
}

func TestSpawnAgentRejectsInvalidRequestsBeforeLaunch(t *testing.T) {
	validExecution := action.Execution{CommandID: id.MessageID()}
	validInput := json.RawMessage(`{"package":"` + actionPackage + `"}`)
	for _, test := range []struct {
		name      string
		execution *action.Execution
		input     json.RawMessage
	}{
		{name: "no execution", input: validInput},
		{name: "empty execution", execution: &action.Execution{}, input: validInput},
		{name: "task without command", execution: &action.Execution{Work: messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID()}}, input: validInput},
		{name: "invalid command", execution: &action.Execution{CommandID: "../command"}, input: validInput},
		{name: "incomplete task", execution: &action.Execution{Work: messages.WorkContext{TaskID: id.TaskID()}}, input: validInput},
		{name: "invalid task with command", execution: &action.Execution{CommandID: id.MessageID(), Work: messages.WorkContext{TaskID: "../task"}}, input: validInput},
		{name: "unsupported channel", execution: &validExecution, input: json.RawMessage(`{"package":"github.com/defos-ai/commander-example-agent@latest"}`)},
		{name: "missing package", execution: &validExecution, input: json.RawMessage(`{}`)},
		{name: "null package", execution: &validExecution, input: json.RawMessage(`{"package":null}`)},
		{name: "invalid visibility", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","visibility":"internal"}`)},
		{name: "empty visibility", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","visibility":""}`)},
		{name: "caller token", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","token":"never-accept-input-secrets"}`)},
		{name: "caller agent ID", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","agent_id":"example-agent-12345678"}`)},
		{name: "caller request ID", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","request_id":"my-request"}`)},
		{name: "caller runtime", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","runtime":"kubernetes"}`)},
		{name: "caller namespace", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","namespace":"agents"}`)},
		{name: "caller service account", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","service_account":"agent-worker"}`)},
		{name: "caller environment", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","env":{"SECRETS_ENCRYPTION_KEY":"value"}}`)},
		{name: "caller arguments", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","args":["--config=other"]}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			launcher := &actionLauncher{}
			executor := newSpawnAction(t, launcher)
			ctx := t.Context()
			if test.execution != nil {
				ctx = action.WithExecution(ctx, *test.execution)
			}
			if _, err := executor.Run(ctx, test.input); err == nil {
				t.Fatal("invalid spawn request was accepted")
			}
			if len(launcher.calls) != 0 {
				t.Fatal("invalid input reached the launcher")
			}
		})
	}
}

func TestSpawnAgentOutputIdentifiesLaunchRuntime(t *testing.T) {
	for _, test := range []struct {
		name    string
		runtime runtimes.RuntimeKind
		details runtimes.RuntimeDetails
		valid   bool
	}{
		{"native process", runtimes.RuntimeNative, runtimes.NativeRuntimeDetails{PID: 42}, true},
		{"docker container", runtimes.RuntimeDocker, runtimes.DockerRuntimeDetails{ContainerID: "fixture-container"}, true},
		{"kubernetes pod", runtimes.RuntimeKubernetes, runtimes.KubernetesRuntimeDetails{Namespace: "agents", PodName: "worker.example-1", PodUID: "opaque:pod/uid"}, true},
		{"missing runtime", "", runtimes.NativeRuntimeDetails{PID: 42}, false},
		{"unsupported runtime", "job", runtimes.NativeRuntimeDetails{PID: 42}, false},
		{"missing details", runtimes.RuntimeNative, nil, false},
		{"native zero pid", runtimes.RuntimeNative, runtimes.NativeRuntimeDetails{}, false},
		{"native negative pid", runtimes.RuntimeNative, runtimes.NativeRuntimeDetails{PID: -1}, false},
		{"native with docker details", runtimes.RuntimeNative, runtimes.DockerRuntimeDetails{ContainerID: "fixture-container"}, false},
		{"native with kubernetes details", runtimes.RuntimeNative, runtimes.KubernetesRuntimeDetails{Namespace: "agents", PodName: "worker", PodUID: "opaque-uid"}, false},
		{"docker empty container", runtimes.RuntimeDocker, runtimes.DockerRuntimeDetails{}, false},
		{"docker with native details", runtimes.RuntimeDocker, runtimes.NativeRuntimeDetails{PID: 42}, false},
		{"docker with kubernetes details", runtimes.RuntimeDocker, runtimes.KubernetesRuntimeDetails{Namespace: "agents", PodName: "worker", PodUID: "opaque-uid"}, false},
		{"kubernetes without namespace", runtimes.RuntimeKubernetes, runtimes.KubernetesRuntimeDetails{PodName: "worker", PodUID: "opaque-uid"}, false},
		{"kubernetes without pod name", runtimes.RuntimeKubernetes, runtimes.KubernetesRuntimeDetails{Namespace: "agents", PodUID: "opaque-uid"}, false},
		{"kubernetes without pod UID", runtimes.RuntimeKubernetes, runtimes.KubernetesRuntimeDetails{Namespace: "agents", PodName: "worker"}, false},
		{"kubernetes with native details", runtimes.RuntimeKubernetes, runtimes.NativeRuntimeDetails{PID: 42}, false},
		{"kubernetes with docker details", runtimes.RuntimeKubernetes, runtimes.DockerRuntimeDetails{ContainerID: "fixture-container"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			launcher := &actionLauncher{result: runtimes.Result{
				AgentID: "example-agent-12345678", AgentType: "example-agent", Package: actionPackage,
				Runtime: test.runtime, RuntimeDetails: test.details, Actions: []actionmodel.Action{},
			}}
			executor := newSpawnAction(t, launcher)
			ctx := action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID()})
			output, err := executor.Run(ctx, json.RawMessage(`{"package":"`+actionPackage+`"}`))
			if (err == nil) != test.valid {
				t.Fatalf("runtime result validity: got %v, want valid=%t", err, test.valid)
			}
			if !test.valid {
				return
			}
			var actual runtimes.Result
			if err := json.Unmarshal(output.Output, &actual); err != nil || !reflect.DeepEqual(actual, launcher.result) {
				t.Fatalf("runtime output did not preserve the typed launch identity: %+v (%v)", actual, err)
			}
		})
	}
}

func TestSpawnAgentOutputValidatesKubernetesNames(t *testing.T) {
	maximumPodName := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	for _, test := range []struct {
		name, namespace, podName string
		valid                    bool
	}{
		{"minimum names", "a", "0", true},
		{"maximum names", strings.Repeat("a", 63), maximumPodName, true},
		{"uppercase namespace", "Agents", "worker", false},
		{"dotted namespace", "agents.example", "worker", false},
		{"namespace leading hyphen", "-agents", "worker", false},
		{"namespace trailing hyphen", "agents-", "worker", false},
		{"namespace too long", strings.Repeat("a", 64), "worker", false},
		{"uppercase pod", "agents", "Worker", false},
		{"pod underscore", "agents", "worker_1", false},
		{"pod path", "agents", "worker/other", false},
		{"pod empty label", "agents", "worker..example", false},
		{"pod trailing dot", "agents", "worker.", false},
		{"pod too long", "agents", maximumPodName + "a", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			launcher := &actionLauncher{result: runtimes.Result{AgentID: "example-agent-12345678", AgentType: "example-agent", Package: actionPackage,
				Runtime: runtimes.RuntimeKubernetes, RuntimeDetails: runtimes.KubernetesRuntimeDetails{Namespace: test.namespace, PodName: test.podName, PodUID: "opaque-uid"}, Actions: []actionmodel.Action{}}}
			executor := newSpawnAction(t, launcher)
			ctx := action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID()})
			if _, err := executor.Run(ctx, json.RawMessage(`{"package":"`+actionPackage+`"}`)); (err == nil) != test.valid {
				t.Fatalf("Kubernetes name validity: got %v, want valid=%t", err, test.valid)
			}
		})
	}
}

func TestSpawnAgentAdvertisedSchemaRejectsInvalidRuntimeDetails(t *testing.T) {
	executor := newSpawnAction(t, &actionLauncher{})
	schema, err := actionmodel.ResolveSchema(executor.Action().OutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		runtime string
		details any
		extra   string
	}{
		{"missing details", "native", nil, ""},
		{"details null", "native", json.RawMessage(`null`), ""},
		{"details array", "native", []any{42}, ""},
		{"details string", "native", "pid=42", ""},
		{"native missing pid", "native", map[string]any{}, ""},
		{"native foreign container", "native", map[string]any{"pid": 42, "container_id": "container"}, ""},
		{"native foreign namespace", "native", map[string]any{"pid": 42, "namespace": "agents"}, ""},
		{"native unknown field", "native", map[string]any{"pid": 42, "other": true}, ""},
		{"docker missing container", "docker", map[string]any{}, ""},
		{"docker empty container", "docker", map[string]any{"container_id": ""}, ""},
		{"docker foreign pid", "docker", map[string]any{"container_id": "container", "pid": 42}, ""},
		{"kubernetes missing namespace", "kubernetes", map[string]any{"pod_name": "worker", "pod_uid": "uid"}, ""},
		{"kubernetes missing name", "kubernetes", map[string]any{"namespace": "agents", "pod_uid": "uid"}, ""},
		{"kubernetes missing UID", "kubernetes", map[string]any{"namespace": "agents", "pod_name": "worker"}, ""},
		{"kubernetes empty namespace", "kubernetes", map[string]any{"namespace": "", "pod_name": "worker", "pod_uid": "uid"}, ""},
		{"kubernetes empty name", "kubernetes", map[string]any{"namespace": "agents", "pod_name": "", "pod_uid": "uid"}, ""},
		{"kubernetes empty UID", "kubernetes", map[string]any{"namespace": "agents", "pod_name": "worker", "pod_uid": ""}, ""},
		{"kubernetes foreign pid", "kubernetes", map[string]any{"namespace": "agents", "pod_name": "worker", "pod_uid": "uid", "pid": 42}, ""},
		{"kubernetes foreign container", "kubernetes", map[string]any{"namespace": "agents", "pod_name": "worker", "pod_uid": "uid", "container_id": "container"}, ""},
		{"old flat pid", "native", map[string]any{"pid": 42}, "pid"},
		{"old flat container", "docker", map[string]any{"container_id": "container"}, "container_id"},
		{"old flat namespace", "kubernetes", map[string]any{"namespace": "agents", "pod_name": "worker", "pod_uid": "uid"}, "namespace"},
		{"old flat pod name", "kubernetes", map[string]any{"namespace": "agents", "pod_name": "worker", "pod_uid": "uid"}, "pod_name"},
		{"old flat pod UID", "kubernetes", map[string]any{"namespace": "agents", "pod_name": "worker", "pod_uid": "uid"}, "pod_uid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := map[string]any{"agent_id": "example-agent-12345678", "agent_type": "example-agent", "package": actionPackage, "runtime": test.runtime, "actions": []any{}}
			if test.details != nil {
				output["runtime_details"] = test.details
			}
			if test.extra != "" {
				output[test.extra] = "obsolete"
			}
			// Decode the same JSON shape a client receives before checking the
			// registered schema, including an explicitly present null payload.
			raw, err := json.Marshal(output)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &output); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(output); err == nil {
				t.Fatal("advertised output schema accepted invalid runtime details")
			}
		})
	}
}

func TestSpawnAgentChecksDiscoveryCancellationAndLaunchFailure(t *testing.T) {
	failure := errors.New("launch outcome is uncertain")
	launcher := &actionLauncher{err: failure}
	executor, err := spawneractions.NewSpawnAgent(launcher)
	if err != nil {
		t.Fatal(err)
	}
	ctx := action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID()})
	input := json.RawMessage(`{"package":"` + actionPackage + `"}`)
	if _, err := executor.Run(ctx, input); err == nil || len(launcher.calls) != 0 {
		t.Fatal("spawn action ran without discovery")
	}
	if _, err := executor.Discover(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := executor.Run(canceled, input); !errors.Is(err, context.Canceled) || len(launcher.calls) != 0 {
		t.Fatalf("canceled spawn request reached the launcher: %v", err)
	}
	if _, err := executor.Run(ctx, input); !errors.Is(err, failure) || len(launcher.calls) != 1 {
		t.Fatalf("launch failure was hidden or retried: %v", err)
	}
}

func TestSpawnerAgentWorkRequiresControllerDispatch(t *testing.T) {
	launcher := &actionLauncher{result: runtimes.Result{AgentID: "example-agent-12345678", AgentType: "example-agent",
		Package: actionPackage, Runtime: runtimes.RuntimeNative, RuntimeDetails: runtimes.NativeRuntimeDetails{PID: 42}, Actions: []actionmodel.Action{}}}
	runtime, err := spawner.NewAgent(agent.AgentConfig{ArtifactFileStore: actionArtifacts{},
		Server: agent.ServerInfo{Hostname: "fixture", IPAddress: "127.0.0.1"}}, launcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID(), StepID: "provision"}
	ctx := action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID(), Work: work})
	_, err = runtime.Work(ctx, agent.ActionCall{ActionName: spawneractions.SpawnAgentName, Work: work,
		Input: json.RawMessage(`{"package":"` + actionPackage + `"}`)})
	if err == nil || len(launcher.calls) != 0 {
		t.Fatalf("standalone Work must not launch without a controller execution: %v", err)
	}
}

func newSpawnAction(t *testing.T, launcher spawneractions.Spawner) action.Executor {
	t.Helper()
	executor, err := spawneractions.NewSpawnAgent(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Discover(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
	return executor
}

type spawnInvocation struct{ requestID, reference string }

type actionLauncher struct {
	calls  []spawnInvocation
	result runtimes.Result
	err    error
}

func (launcher *actionLauncher) Spawn(_ context.Context, requestID, reference string) (runtimes.Result, error) {
	launcher.calls = append(launcher.calls, spawnInvocation{requestID: requestID, reference: reference})
	return launcher.result, launcher.err
}

type actionArtifacts struct{}

func (actionArtifacts) Put(context.Context, string, artifacts.FileSource) (artifacts.ArtifactFile, error) {
	return artifacts.ArtifactFile{}, errors.New("unexpected artifact write")
}

func (actionArtifacts) Get(context.Context, string, artifacts.ArtifactFile) ([]byte, error) {
	return nil, errors.New("unexpected artifact read")
}

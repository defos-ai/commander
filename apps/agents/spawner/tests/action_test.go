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
	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

func TestSpawnerAgentHasIndependentCatalogs(t *testing.T) {
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{})
	config := agent.AgentConfig{Type: "ignored", ArtifactFileStore: fakes.RejectArtifacts{},
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
	if len(launcher.Calls()) != 0 {
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
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: runtimes.Result{
		AgentID: "example-agent-12345678", AgentType: "example-agent", Package: fixtures.ActionPackage, Runtime: runtimes.RuntimeNative, RuntimeDetails: runtimes.NativeRuntimeDetails{PID: 42},
		Actions: []actionmodel.Action{{Name: "inspect", Description: "Inspect a local target", Status: action.Unavailable,
			MissingRequirements: []actionmodel.Requirement{{Kind: actionmodel.ExecutableRequirement, Name: "example-tool"}},
			InputSchema:         json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}},
	}})
	executor := fixtures.SpawnAction(t, launcher)
	work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID(), StepID: "provision"}
	for _, execution := range []action.Execution{{CommandID: id.MessageID(), Work: work}, {CommandID: id.MessageID()}} {
		for _, reference := range []string{
			fixtures.ActionPackage,
			"github.com/defos-ai/commander-example-agent",
			"https://github.com/defos-ai/commander-example-agent",
			"github.com/defos-ai/commander-example-agent@stable",
			"github.com/defos-ai/commander-example-agent@v0.1.0",
			"github.com/defos-ai/commander/apps/agents/example@stable",
			"https://" + fixtures.ActionPackage,
			"https://github.com/DeFoS-AI/commander-example-agent@v0.1.0",
		} {
			ctx := action.WithExecution(t.Context(), execution)
			input := json.RawMessage(`{"package":"` + reference + `"}`)

			result, err := executor.Run(ctx, input)

			if err != nil {
				t.Fatal(err)
			}
			var actual runtimes.Result
			if err := json.Unmarshal(result.Output, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, launcher.Result()) {
				t.Fatalf("spawn output lost registration or action readiness: %+v", actual)
			}
			wantID := "command:" + execution.CommandID
			if call := launcher.Calls()[len(launcher.Calls())-1]; call != (fakes.SpawnInvocation{RequestID: wantID, Reference: reference}) {
				t.Fatalf("launcher received wrong dispatch identity: %+v", call)
			}
		}
	}
}

func TestSpawnAgentApprovalFlagsAreNotActionInputs(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "approval-field-test-token")
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: fixtures.SpawnResult()})
	executor := fixtures.SpawnAction(t, launcher)
	schema, err := actionmodel.ResolveSchema(executor.Action().InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{fixtures.ActionPackage, "github.com/acme/worker-agent@v1.2.3"} {
		for _, field := range []string{"confirmed", "approved", "approval_question_id"} {
			t.Run(reference+"/"+field, func(t *testing.T) {
				values := map[string]any{"package": reference, "visibility": "private", field: true}
				input, err := json.Marshal(values)
				if err != nil {
					t.Fatal(err)
				}
				ctx := action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID()})

				_, runErr := executor.Run(ctx, input)
				schemaErr := schema.Validate(values)

				if runErr == nil || schemaErr == nil || errors.Is(runErr, packages.ErrSourceApprovalRequired) || len(launcher.Calls()) != 0 {
					t.Fatalf("caller approval flag bypassed strict action decoding or schema: run=%v schema=%v", runErr, schemaErr)
				}
			})
		}
	}
}

func TestSpawnAgentValidatesExecutionBeforeSourcePolicy(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{})
	executor := fixtures.SpawnAction(t, launcher)
	input := json.RawMessage(`{"package":"github.com/acme/worker-agent@v1.2.3","visibility":"private"}`)

	_, err := executor.Run(t.Context(), input)

	if err == nil || !strings.Contains(err.Error(), "controller-dispatched execution") || errors.Is(err, packages.ErrSourceApprovalRequired) || len(launcher.Calls()) != 0 {
		t.Fatalf("source policy bypassed execution validation: %v", err)
	}
}

func TestSpawnAgentRejectsInvalidRequestsBeforeLaunch(t *testing.T) {
	validExecution := action.Execution{CommandID: id.MessageID()}
	validInput := json.RawMessage(`{"package":"` + fixtures.ActionPackage + `"}`)
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
		{name: "invalid visibility", execution: &validExecution, input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `","visibility":"internal"}`)},
		{name: "empty visibility", execution: &validExecution, input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `","visibility":""}`)},
		{name: "caller token", execution: &validExecution, input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `","token":"never-accept-input-secrets"}`)},
		{name: "caller agent ID", execution: &validExecution, input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `","agent_id":"example-agent-12345678"}`)},
		{name: "caller request ID", execution: &validExecution, input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `","request_id":"my-request"}`)},
		{name: "caller runtime", execution: &validExecution, input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `","runtime":"kubernetes"}`)},
		{name: "caller namespace", execution: &validExecution, input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `","namespace":"agents"}`)},
		{name: "caller service account", execution: &validExecution, input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `","service_account":"agent-worker"}`)},
		{name: "caller environment", execution: &validExecution, input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `","env":{"SECRETS_ENCRYPTION_KEY":"value"}}`)},
		{name: "caller arguments", execution: &validExecution, input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `","args":["--config=other"]}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{})
			executor := fixtures.SpawnAction(t, launcher)
			ctx := t.Context()
			if test.execution != nil {
				ctx = action.WithExecution(ctx, *test.execution)
			}
			_, err := executor.Run(ctx, test.input)

			if err == nil {
				t.Fatal("invalid spawn request was accepted")
			}
			if len(launcher.Calls()) != 0 {
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
			launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: runtimes.Result{
				AgentID: "example-agent-12345678", AgentType: "example-agent", Package: fixtures.ActionPackage,
				Runtime: test.runtime, RuntimeDetails: test.details, Actions: []actionmodel.Action{},
			}})
			executor := fixtures.SpawnAction(t, launcher)
			ctx := action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID()})
			output, err := executor.Run(ctx, json.RawMessage(`{"package":"`+fixtures.ActionPackage+`"}`))
			if (err == nil) != test.valid {
				t.Fatalf("runtime result validity: got %v, want valid=%t", err, test.valid)
			}
			if !test.valid {
				return
			}
			var actual runtimes.Result
			if err := json.Unmarshal(output.Output, &actual); err != nil || !reflect.DeepEqual(actual, launcher.Result()) {
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
			launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: runtimes.Result{AgentID: "example-agent-12345678", AgentType: "example-agent", Package: fixtures.ActionPackage,
				Runtime: runtimes.RuntimeKubernetes, RuntimeDetails: runtimes.KubernetesRuntimeDetails{Namespace: test.namespace, PodName: test.podName, PodUID: "opaque-uid"}, Actions: []actionmodel.Action{}}})
			executor := fixtures.SpawnAction(t, launcher)
			ctx := action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID()})
			if _, err := executor.Run(ctx, json.RawMessage(`{"package":"`+fixtures.ActionPackage+`"}`)); (err == nil) != test.valid {
				t.Fatalf("Kubernetes name validity: got %v, want valid=%t", err, test.valid)
			}
		})
	}
}

func TestSpawnAgentAdvertisedSchemaRejectsInvalidRuntimeDetails(t *testing.T) {
	executor := fixtures.SpawnAction(t, fakes.NewActionLauncher(fakes.ActionLauncherConfig{}))
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
			output := map[string]any{"agent_id": "example-agent-12345678", "agent_type": "example-agent", "package": fixtures.ActionPackage, "runtime": test.runtime, "actions": []any{}}
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
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Error: failure})
	executor, err := spawneractions.NewSpawnAgent(launcher)
	if err != nil {
		t.Fatal(err)
	}
	ctx := action.WithExecution(t.Context(), action.Execution{CommandID: id.MessageID()})
	input := json.RawMessage(`{"package":"` + fixtures.ActionPackage + `"}`)
	if _, err := executor.Run(ctx, input); err == nil || len(launcher.Calls()) != 0 {
		t.Fatal("spawn action ran without discovery")
	}
	if _, err := executor.Discover(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := executor.Run(canceled, input); !errors.Is(err, context.Canceled) || len(launcher.Calls()) != 0 {
		t.Fatalf("canceled spawn request reached the launcher: %v", err)
	}
	if _, err := executor.Run(ctx, input); !errors.Is(err, failure) || len(launcher.Calls()) != 1 {
		t.Fatalf("launch failure was hidden or retried: %v", err)
	}
}

func TestSpawnerAgentWorkRequiresControllerDispatch(t *testing.T) {
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: runtimes.Result{AgentID: "example-agent-12345678", AgentType: "example-agent",
		Package: fixtures.ActionPackage, Runtime: runtimes.RuntimeNative, RuntimeDetails: runtimes.NativeRuntimeDetails{PID: 42}, Actions: []actionmodel.Action{}}})
	runtime, err := spawner.NewAgent(agent.AgentConfig{ArtifactFileStore: fakes.RejectArtifacts{},
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
		Input: json.RawMessage(`{"package":"` + fixtures.ActionPackage + `"}`)})
	if err == nil || len(launcher.Calls()) != 0 {
		t.Fatalf("standalone Work must not launch without a controller execution: %v", err)
	}
}

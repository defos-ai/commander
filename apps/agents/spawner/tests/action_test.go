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

const actionPackage = "github.com/defos-ai/commander-example-agent@v0.1.0"

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
		AgentID: "example-agent-12345678", AgentType: "example-agent", Package: actionPackage, Runtime: "native", PID: 42,
		Actions: []actionmodel.Action{{Name: "inspect", Description: "Inspect a local target", Status: action.Unavailable,
			MissingRequirements: []actionmodel.Requirement{{Kind: actionmodel.ExecutableRequirement, Name: "example-tool"}},
			InputSchema:         json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}},
	}}
	executor := newSpawnAction(t, launcher)
	work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID(), StepID: "provision"}
	for _, execution := range []action.Execution{{CommandID: id.MessageID(), Work: work}, {CommandID: id.MessageID()}} {
		ctx := action.WithExecution(t.Context(), execution)
		result, err := executor.Run(ctx, json.RawMessage(`{"package":"`+actionPackage+`"}`))
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
		if execution.Work.TaskID != "" {
			wantID = "task:" + execution.Work.TaskID
		}
		if call := launcher.calls[len(launcher.calls)-1]; call != (spawnInvocation{requestID: wantID, reference: actionPackage}) {
			t.Fatalf("launcher received wrong dispatch identity: %+v", call)
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
		{name: "invalid command", execution: &action.Execution{CommandID: "../command"}, input: validInput},
		{name: "incomplete task", execution: &action.Execution{Work: messages.WorkContext{TaskID: id.TaskID()}}, input: validInput},
		{name: "invalid task with command", execution: &action.Execution{CommandID: id.MessageID(), Work: messages.WorkContext{TaskID: "../task"}}, input: validInput},
		{name: "moving package", execution: &validExecution, input: json.RawMessage(`{"package":"github.com/defos-ai/commander-example-agent@stable"}`)},
		{name: "missing package", execution: &validExecution, input: json.RawMessage(`{}`)},
		{name: "null package", execution: &validExecution, input: json.RawMessage(`{"package":null}`)},
		{name: "caller agent ID", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","agent_id":"example-agent-12345678"}`)},
		{name: "caller request ID", execution: &validExecution, input: json.RawMessage(`{"package":"` + actionPackage + `","request_id":"my-request"}`)},
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
		name, runtime, containerID string
		pid                        int
		valid                      bool
	}{
		{name: "native process", runtime: "native", pid: 42, valid: true},
		{name: "docker container", runtime: "docker", containerID: "fixture-container", valid: true},
		{name: "missing runtime", pid: 42},
		{name: "native without pid", runtime: "native"},
		{name: "native with container", runtime: "native", pid: 42, containerID: "fixture-container"},
		{name: "docker without container", runtime: "docker"},
		{name: "docker with pid", runtime: "docker", pid: 42, containerID: "fixture-container"},
	} {
		t.Run(test.name, func(t *testing.T) {
			launcher := &actionLauncher{result: runtimes.Result{
				AgentID: "example-agent-12345678", AgentType: "example-agent", Package: actionPackage,
				Runtime: test.runtime, PID: test.pid, ContainerID: test.containerID, Actions: []actionmodel.Action{},
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
			var properties map[string]json.RawMessage
			if err := json.Unmarshal(output.Output, &properties); err != nil {
				t.Fatal(err)
			}
			if test.runtime == "native" && properties["container_id"] != nil || test.runtime == "docker" && properties["pid"] != nil {
				t.Fatal("result exposed the other runtime's process identifier")
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

func TestSpawnerAgentWorkUsesTaskIdentity(t *testing.T) {
	launcher := &actionLauncher{result: runtimes.Result{AgentID: "example-agent-12345678", AgentType: "example-agent",
		Package: actionPackage, Runtime: "native", PID: 42, Actions: []actionmodel.Action{}}}
	runtime, err := spawner.NewAgent(agent.AgentConfig{ArtifactFileStore: actionArtifacts{},
		Server: agent.ServerInfo{Hostname: "fixture", IPAddress: "127.0.0.1"}}, launcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	work := messages.WorkContext{OperationID: id.OperationID(), TaskID: id.TaskID(), StepID: "provision"}
	_, err = runtime.Work(t.Context(), agent.ActionCall{ActionName: spawneractions.SpawnAgentName, Work: work,
		Input: json.RawMessage(`{"package":"` + actionPackage + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.calls) != 1 || launcher.calls[0].requestID != "task:"+work.TaskID {
		t.Fatalf("agent did not supply task identity: %+v", launcher.calls)
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

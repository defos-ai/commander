package mcp_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSpawnerRegistersAnAgentAndExecutesItsAction(t *testing.T) {
	a := connect(t)
	const reference = "github.com/defos-ai/fixture-agent@v0.1.0"
	var provision operations.Operation
	input := object{"package": reference}
	a.call("start_operation", object{"mission_name": "fixture-spawn-agent", "input": input}, &provision)
	if provision.Preflight == nil || !provision.Preflight.Ready {
		t.Fatalf("spawner is not ready: %+v", provision.Preflight)
	}
	var assigned operations.Task
	a.call("assign_task", object{"operation_id": provision.ID, "action_name": "spawn-agent", "input": input}, &assigned)
	spawnTask := waitSpawnerTask(t, a, provision.ID, assigned.ID)
	var spawned struct {
		AgentID     string           `json:"agent_id"`
		AgentType   string           `json:"agent_type"`
		Package     string           `json:"package"`
		Runtime     string           `json:"runtime"`
		PID         int              `json:"pid"`
		ContainerID string           `json:"container_id"`
		Actions     []actions.Action `json:"actions"`
	}
	if err := json.Unmarshal(spawnTask.Result.Output, &spawned); err != nil {
		t.Fatal(err)
	}
	if !id.ValidAgent(spawned.AgentID) || spawned.AgentType != "fixture-agent" || spawned.Package != reference || len(spawned.Actions) != 1 || spawned.Actions[0].Name != "fixture-echo" || spawned.Actions[0].Status != actions.Ready {
		t.Fatalf("spawn result lost the registered identity or capability: %+v", spawned)
	}
	if !((spawned.Runtime == "native" && spawned.PID > 0 && spawned.ContainerID == "") || (spawned.Runtime == "docker" && spawned.PID == 0 && spawned.ContainerID != "")) {
		t.Fatalf("spawn result does not identify the launch runtime: %+v", spawned)
	}
	expectedRuntime := os.Getenv("COMMANDER_TEST_SPAWNER_RUNTIME")
	if expectedRuntime == "" {
		expectedRuntime = "native"
	}
	if spawned.Runtime != expectedRuntime {
		t.Fatalf("spawned with runtime %q, expected %q", spawned.Runtime, expectedRuntime)
	}
	waitFixtureAgent(t, a, spawned.AgentID)
	a.call("complete_operation", object{"operation_id": provision.ID, "output": object{"agent_id": spawned.AgentID}}, nil)

	// A separate mission can now use the capability. Previous API/MCP scenarios
	// may have spawned another instance; the controller chooses the executor.
	var operation operations.Operation
	message := "MCP spawned agent " + spawned.AgentID
	input = object{"message": message}
	a.call("start_operation", object{"mission_name": "fixture-use-agent", "input": input}, &operation)
	if operation.Preflight == nil || !operation.Preflight.Ready || len(operation.Preflight.Actions) != 1 || operation.Preflight.Actions[0].ActionName != "fixture-echo" || !slices.Contains(operation.Preflight.Actions[0].ReadyAgentIDs, spawned.AgentID) {
		t.Fatalf("spawned capability is absent from preflight: %+v", operation.Preflight)
	}
	a.call("assign_task", object{"operation_id": operation.ID, "action_name": "fixture-echo", "input": input}, &assigned)
	executed := waitSpawnerTask(t, a, operation.ID, assigned.ID)
	var output struct {
		Message string `json:"message"`
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(executed.Result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.Message != message || output.AgentID != executed.AgentID {
		t.Fatalf("accepted result does not match its input and executor: task=%+v output=%+v", executed, output)
	}
	waitFixtureAgent(t, a, executed.AgentID)
	if len(executed.Result.Artifacts) != 1 {
		t.Fatalf("spawned agent did not upload its artifact: %+v", executed.Result)
	}
	artifact := executed.Result.Artifacts[0]
	if artifact.AgentID != executed.AgentID || artifact.CommandID != executed.CommandID || artifact.ActionName != "fixture-echo" || len(artifact.Files) != 1 || artifact.Files[0].Name != "echo.txt" {
		t.Fatalf("spawned agent artifact lost ownership or file metadata: %+v", artifact)
	}
	resource, err := a.session.ReadResource(a.ctx, &mcp.ReadResourceParams{URI: "missions://artifacts/" + artifact.ID + "/files/" + artifact.Files[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(resource.Contents) != 1 || resource.Contents[0].MIMEType != "text/plain" || resource.Contents[0].Text != message {
		t.Fatalf("spawned agent artifact bytes did not reach storage: %+v", resource)
	}
	a.call("complete_operation", object{"operation_id": operation.ID, "output": object{"message": output.Message, "agent_id": output.AgentID}}, nil)
}

func waitSpawnerTask(t *testing.T, a *api, operationID, taskID string) operations.Task {
	t.Helper()
	finished := a.wait(operationID, func(op operationDetail) bool {
		for _, task := range op.Tasks {
			if task.ID == taskID {
				return task.Status != operations.TaskQueued && task.Status != operations.TaskRunning
			}
		}
		return false
	})
	for _, task := range finished.Tasks {
		if task.ID == taskID {
			if task.Status != operations.TaskSucceeded || task.Result == nil {
				t.Fatalf("spawned-agent workflow failed: %+v", task)
			}
			return task
		}
	}
	t.Fatal("accepted task is absent from operation")
	return operations.Task{}
}

func waitFixtureAgent(t *testing.T, a *api, agentID string) {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var inventory struct {
			Agents []agents.Agent `json:"agents"`
		}
		a.call("list_agents", object{}, &inventory)
		for _, agent := range inventory.Agents {
			if agent.ID == agentID && agent.Type == "fixture-agent" && !agent.Stale && agent.Status == agents.AgentReady && agent.Work == nil && len(agent.Actions) == 1 && agent.Actions[0].Name == "fixture-echo" && agent.Actions[0].Status == actions.Ready {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("controller did not persist ready fixture agent %s: %+v", agentID, inventory.Agents)
		case <-a.ctx.Done():
			t.Fatal(a.ctx.Err())
		case <-tick.C:
		}
	}
}

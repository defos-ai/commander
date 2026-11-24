package operation_test

import (
	"testing"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/operation"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/operation"
)

func TestTaskSelectorRoutesToHostnameAndRetainsIndependentCriteria(t *testing.T) {
	config := fixtures.Config()
	native := fixtures.ReadyAgentOnHost(fixtures.AgentID, "native-host")
	target := fixtures.ReadyAgentOnHost("review-agent-00000002", "k8s-prod")
	config.AgentCatalog.(*fakes.AgentCatalog).Set(native, target)
	exec, calls := fakes.ControlledExecutor()
	r := fixtures.NewWorker(t, config, exec)
	op := fixtures.Start(t, r)
	selector := &agents.Selector{Hostname: target.Server.Hostname}

	_, rejected := r.Assign(t.Context(), commands.AssignTaskCommand{
		OperationID: op.ID, ActionName: "inspect", Input: []byte(`{}`), Selector: &agents.Selector{},
	})
	before, readErr := r.Get(t.Context(), op.ID)
	if rejected == nil || readErr != nil || len(before.Tasks) != 0 {
		t.Fatalf("empty selector admitted work before valid routing: operation=%+v, assign=%v, read=%v", before, rejected, readErr)
	}

	task, err := r.Assign(t.Context(), commands.AssignTaskCommand{
		OperationID: op.ID, ActionName: "inspect", Input: []byte(`{}`), Selector: selector,
	})
	if err != nil {
		t.Fatal(err)
	}
	selector.Hostname = native.Server.Hostname
	if task.Selector == nil {
		t.Fatal("accepted task omitted its selector")
	}
	task.Selector.Hostname = "changed-returned-task"
	call := fixtures.Receive(t, calls)

	if call.Agent != target.ID || call.Call.TaskID != task.ID || string(call.Call.Input) != `{}` {
		t.Fatalf("selector did not route unchanged action input to its host: %+v", call)
	}
	call.Reply <- fixtures.Output("inspect")
	settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return op.Tasks[0].Status == operations.TaskSucceeded
	})
	got := settled.Tasks[0]
	if got.Selector == nil || got.Selector.Hostname != target.Server.Hostname || got.AgentID != target.ID {
		t.Fatalf("caller or returned-task mutation changed accepted routing: %+v", got)
	}
}

func TestUnavailableTaskSelectorStaysQueuedWithoutBlockingOtherAssignments(t *testing.T) {
	for _, state := range []string{"missing", "stale", "stopping", "unready", "unavailable action", "missing action", "missing requirements"} {
		t.Run(state, func(t *testing.T) {
			config := fixtures.Config()
			missions := config.MissionStore.(*fakes.MissionStore)
			mission := missions.Snapshot()
			mission.RequiredActions = []string{"inspect"}
			missions.Set(mission)
			native := fixtures.ReadyAgentOnHost(fixtures.AgentID, "native-host")
			target := fixtures.ReadyAgentOnHost("review-agent-00000002", "k8s-prod")
			unavailable := target.Clone()
			switch state {
			case "missing":
				unavailable.Server.Hostname = "another-host"
			case "stale":
				unavailable.Stale = true
			case "stopping":
				unavailable.Status = agents.AgentStopRequested
			case "unready":
				unavailable.Status = agents.AgentDiscovering
			case "unavailable action":
				unavailable.Actions[0].Status = actions.Unavailable
				unavailable.Actions[0].StatusReason = "Runtime dependency is unavailable"
			case "missing action":
				unavailable.Actions = unavailable.Actions[1:]
			case "missing requirements":
				unavailable.Actions[0].Status = actions.Unavailable
				unavailable.Actions[0].MissingRequirements = []actions.Requirement{{Kind: actions.ExecutableRequirement, Name: "inspect-runtime"}}
			}
			catalog := config.AgentCatalog.(*fakes.AgentCatalog)
			catalog.Set(native, unavailable)
			exec, calls := fakes.ControlledExecutor()
			r := fixtures.NewWorker(t, config, exec)
			op := fixtures.Start(t, r)

			blockedTask, err := r.Assign(t.Context(), commands.AssignTaskCommand{
				OperationID: op.ID, ActionName: "inspect", Input: []byte(`{}`),
				Selector: &agents.Selector{Hostname: target.Server.Hostname},
			})
			if err != nil {
				t.Fatal(err)
			}

			other := fixtures.Assign(t, r, op.ID, "inspect")
			call := fixtures.Receive(t, calls)
			if call.Call.TaskID != other.ID || call.Agent != native.ID {
				t.Fatalf("blocked task fell back or blocked an eligible assignment: %+v", call)
			}
			call.Reply <- fixtures.Output("inspect")
			retained := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool {
				return len(op.Tasks) == 2 && op.Tasks[1].Status == operations.TaskSucceeded
			})
			if retained.Tasks[0].Status != operations.TaskQueued || retained.Tasks[0].AgentID != "" {
				t.Fatalf("unrelated assignment replaced the blocked routing criteria: %+v", retained.Tasks[0])
			}

			catalog.Set(native, target)
			routed := fixtures.Receive(t, calls)
			if routed.Call.TaskID != blockedTask.ID || routed.Agent != target.ID {
				t.Fatalf("newly ready host did not receive its saved task: %+v", routed)
			}
			routed.Reply <- fixtures.Output("inspect")
			fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool {
				return op.Tasks[0].Status == operations.TaskSucceeded
			})
		})
	}
}

package packages_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

func TestSpawnAgentReportsUnavailableActionsWithoutLosingRegistration(t *testing.T) {
	fixture := fixtures.NewDockerManagerWithUnavailableAction(t)
	executor := fixtures.SpawnAction(t, fixture.Manager)
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	definition := fixtures.UnavailableSpawnAction()

	result, err := executor.Run(ctx, json.RawMessage(`{"package":"`+fixtures.Reference+`"}`))

	if err != nil {
		t.Fatalf("an unavailable action prevented registration: %v", err)
	}
	var output struct {
		AgentID        string                        `json:"agent_id"`
		AgentType      string                        `json:"agent_type"`
		Package        string                        `json:"package"`
		Runtime        runtimes.RuntimeKind          `json:"runtime"`
		RuntimeDetails runtimes.DockerRuntimeDetails `json:"runtime_details"`
		Actions        []struct {
			SessionMode         string                    `json:"session_mode"`
			Name                string                    `json:"name"`
			Status              actionmodel.Status        `json:"status"`
			StatusReason        string                    `json:"status_reason"`
			MissingRequirements []actionmodel.Requirement `json:"missing_requirements"`
			RequiredSecrets     []string                  `json:"required_secrets"`
		} `json:"actions"`
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		t.Fatalf("spawn output included metadata beyond identity and readiness: %v", err)
	}
	if !id.ValidAgent(output.AgentID) || output.AgentType != "fixture-agent" || output.Package != fixtures.Reference || output.Runtime != runtimes.RuntimeDocker || output.RuntimeDetails.ContainerID == "" || len(output.Actions) != 1 || len(result.NextActions) != 0 {
		t.Fatalf("spawn lost the registered identity or produced unexpected work: %s", result.Output)
	}
	readiness := output.Actions[0]
	if readiness.Name != definition.Name || readiness.SessionMode != definition.SessionMode || readiness.Status != actionmodel.Unavailable || readiness.StatusReason != definition.StatusReason || !reflect.DeepEqual(readiness.MissingRequirements, definition.MissingRequirements) || !slices.Equal(readiness.RequiredSecrets, definition.RequiredSecrets) {
		t.Fatalf("spawn hid the unavailable action's prerequisites: %+v", readiness)
	}

	retained, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

	if err != nil {
		t.Fatalf("retry could not observe the registered instance: %v", err)
	}
	if retained.AgentID != output.AgentID || retained.RuntimeDetails != output.RuntimeDetails || !reflect.DeepEqual(retained.Actions, []actionmodel.Action{definition}) {
		t.Fatalf("compact action output changed the retained full registration: %+v", retained)
	}
	creates, starts, _ := fixture.Engine.Counts()
	if creates != 1 || starts != 1 {
		t.Fatalf("observing an unavailable action launched another instance: create=%d start=%d", creates, starts)
	}
}

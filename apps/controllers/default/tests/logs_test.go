package default_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/apps/controllers/default/tests/fixtures"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/domain/executions"
)

func TestCLIPersistsExecutionLogsWithoutChangingExecution(t *testing.T) {
	fixture := fixtures.NewLogController(t)
	message, err := controllerevents.NewExecutionLog(fixture.Log)
	if err != nil {
		t.Fatal(err)
	}

	if err := fixture.Events.Publish(fixture.Context, message); err != nil {
		t.Fatal(err)
	}

	var saved executions.Log
	fixtures.Await(t, fixture.Context, "persisted execution log", func() bool {
		var err error
		saved, err = fixture.Executions.GetLog(fixture.Context, fixture.Log.ID)
		if errors.Is(err, executions.ErrUnknownLog) {
			return false
		}
		if err != nil {
			t.Fatalf("read execution log: %v", err)
		}
		return true
	})
	if !saved.Equal(fixture.Log) {
		t.Fatalf("persisted execution log changed: got=%+v want=%+v", saved, fixture.Log)
	}
	claim, err := fixture.Executions.Get(fixture.Context, fixture.Log.AgentID, fixture.Log.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(claim, fixture.Claim) {
		t.Fatalf("execution log changed its reservation, checkpoints or outcome: got=%+v want=%+v", claim, fixture.Claim)
	}
}

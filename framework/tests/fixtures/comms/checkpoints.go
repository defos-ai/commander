package fixtures

import (
	"encoding/json"
	"testing"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/id"
)

func CheckpointWireRequest(t *testing.T) messages.Message {
	t.Helper()
	request, err := controllerevents.NewCheckpointRequest("fixture-agent-12345678",
		messages.WorkContext{OperationID: id.New(), TaskID: id.New()},
		controllerevents.CheckpointRequest{CommandID: id.New(), Name: "launch", Operation: controllerevents.CheckpointSave,
			Data: json.RawMessage(`{"agent_id":"fixture-agent-87654321"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	adapterevents "commander/framework/comms/clients/adapter/events"
	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/comms/messages"
)

func TestControllerValidatesMethodSpecificRequests(t *testing.T) {
	// Arrange: raw bus requests bypass client validation and reach the real controller.
	d := arrange(t)
	p := d.start(t, false)
	const adapterID = "mcp-adapter-1234abcd"
	replies, err := d.bus.Subscribe(d.ctx, messages.AdapterReplies(adapterID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replies.Close() })
	cases := []struct{ method, parameters, want string }{
		{controllercommands.MethodStartOperation, `{"start":{"mission_name":"review","input":{}}}`, "unknown field"},
		{controllercommands.MethodStartOperation, `{"mission_name":"review","input":{},"operation_id":"bad"}`, "operation_id"},
		{controllercommands.MethodCancelOperation, `{"operation_id":"713bf7e8-1768-4e7e-a85f-b8c9f2aa21f3","input":{}}`, "unknown field"},
		{controllercommands.MethodAssignTask, `{"operation_id":"713bf7e8-1768-4e7e-a85f-b8c9f2aa21f3"}`, "action_name"},
		{controllercommands.MethodStopAgent, `null`, "JSON object"},
	}
	for _, test := range cases {
		// Act: send a valid envelope containing invalid parameters for its method.
		request, err := messages.NewAdapterMessage(messages.AdapterRequest, adapterID, controllercommands.CommandEnvelope{
			Method: test.method, Parameters: json.RawMessage(test.parameters), Deadline: time.Now().Add(5 * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := d.bus.Publish(d.ctx, messages.ControllerRequests("default"), request); err != nil {
			t.Fatal(err)
		}
		reply, err := replies.Next(d.ctx)
		if err != nil {
			t.Fatal(err)
		}
		var result adapterevents.ControllerReply
		if err := json.Unmarshal(reply.Payload, &result); err != nil {
			t.Fatal(err)
		}

		// Assert: invalid parameters receive an ordinary error reply without stopping consumption.
		if reply.CorrelationID != request.ID || result.Error == nil || !strings.Contains(result.Error.Message, test.want) {
			t.Fatalf("%s parameters %s: unexpected reply %+v", test.method, test.parameters, result)
		}
		if err := replies.Ack(d.ctx, reply.ID); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := d.database.OperationStore.List(d.ctx)
	if err != nil || len(stored) != 0 {
		t.Fatalf("invalid requests changed operations: %+v, %v", stored, err)
	}
	p.stop(t)
}

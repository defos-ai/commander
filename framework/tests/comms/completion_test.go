package comms_test

import (
	"encoding/json"
	"errors"
	"testing"

	adapterevents "github.com/defos-ai/commander/framework/comms/clients/adapter/events"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestCompletionPolicyErrorSurvivesControllerReply(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		err    error
		target error
		code   string
	}{
		{"operator required", operations.ErrCompletionOperatorRequired, operations.ErrCompletionOperatorRequired, "completion_operator_required"},
		{"uncertain write takes precedence", errors.Join(operations.ErrCompletionOperatorRequired, operations.ErrWriteUncertain), operations.ErrWriteUncertain, "operation_write_uncertain"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			wire := adapterevents.EncodeError(scenario.err)
			payload, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			var reply adapterevents.ControllerError
			if err := json.Unmarshal(payload, &reply); err != nil {
				t.Fatal(err)
			}

			decoded := adapterevents.DecodeError(&reply)

			if reply.Code != scenario.code || !errors.Is(decoded, scenario.target) {
				t.Fatalf("controller reply lost the completion error classification: %+v, %v", reply, decoded)
			}
		})
	}
}

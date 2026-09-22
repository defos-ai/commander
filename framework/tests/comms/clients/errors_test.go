package clients_test

import (
	"context"
	"errors"
	"testing"

	adapterevents "commander/framework/comms/clients/adapter/events"
	"commander/framework/domain/executions"
)

func TestErrorClassificationPreservesUnknownOutcome(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			// Arrange: a timeout or cancellation does not prove remote work failed.
			original := errors.Join(executions.ErrIndeterminate, cause)
			for range 100 {
				// Act: classify and reconstruct the error as an adapter would.
				wire := adapterevents.EncodeError(original)
				decoded := adapterevents.DecodeError(wire)

				// Assert: classification is stable and retains the unknown outcome.
				if wire.Code != "indeterminate" || !errors.Is(decoded, executions.ErrIndeterminate) || decoded.Error() != original.Error() {
					t.Fatalf("classification lost: %+v, %v", wire, decoded)
				}
			}
			wire := adapterevents.EncodeError(cause)
			if !errors.Is(adapterevents.DecodeError(wire), cause) {
				t.Fatalf("standalone cause changed: %+v", wire)
			}
		})
	}
}

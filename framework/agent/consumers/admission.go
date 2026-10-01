package consumers

import (
	"context"
	"fmt"
	"time"

	"commander/framework/comms/messages"
)

func commandAdmissionError(command messages.Message) error {
	if !command.ExpiresAt.IsZero() && !time.Now().Before(command.ExpiresAt) {
		return fmt.Errorf("command expired before execution started: %w", context.DeadlineExceeded)
	}
	return nil
}

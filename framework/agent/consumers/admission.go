package consumers

import (
	"context"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
)

func commandAdmissionError(command messages.Message) error {
	return checkAdmissionDeadline(command.ExpiresAt)
}

type commandDeadlineKey struct{}

func withCommandDeadline(ctx context.Context, command messages.Message) context.Context {
	return context.WithValue(ctx, commandDeadlineKey{}, command.ExpiresAt)
}

// CheckCommandDeadline rechecks admission after waiting for internal discovery.
// The deadline limits starting execution; it does not cancel work already started.
func CheckCommandDeadline(ctx context.Context) error {
	return checkAdmissionDeadline(CommandDeadline(ctx))
}

// CommandDeadline is admission metadata, not cancellation of a running action.
func CommandDeadline(ctx context.Context) time.Time {
	deadline, _ := ctx.Value(commandDeadlineKey{}).(time.Time)
	return deadline
}

func checkAdmissionDeadline(deadline time.Time) error {
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return fmt.Errorf("command expired before execution started: %w", context.DeadlineExceeded)
	}
	return nil
}

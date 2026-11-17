package agents

import (
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

func ValidateStopRequest(commandID string, requestedAt time.Time) error {
	if !id.Valid(commandID) || requestedAt.IsZero() {
		return errors.New("agent stop requires a command UUID and request timestamp")
	}
	return nil
}

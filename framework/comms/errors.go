package comms

import (
	"errors"
	"fmt"
)

// ErrInvalidMessage marks rejected input. Consumers can retain it while allowing
// other messages to proceed. Persistence and transport failures remain distinct.
var ErrInvalidMessage = errors.New("invalid message")

func InvalidMessage(err error) error {
	return fmt.Errorf("%w: %w", ErrInvalidMessage, err)
}

// Package domain holds shared models and contracts independent of hosted runtimes.
package domain

import "errors"

// ErrControllerClosed is returned when command coordination is unavailable.
var ErrControllerClosed = errors.New("controller is closed")

// Package domain holds shared models and contracts independent of hosted runtimes.
package domain

import "errors"

var ErrControllerClosed = errors.New("controller is closed")

var ErrInvalidInput = errors.New("invalid input")

var ErrInvalidOutput = errors.New("invalid output")

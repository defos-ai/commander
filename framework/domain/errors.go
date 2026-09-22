// Package domain holds shared models and contracts independent of hosted runtimes.
package domain

import "errors"

var ErrControllerClosed = errors.New("controller is closed")

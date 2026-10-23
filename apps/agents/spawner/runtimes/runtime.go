// Package runtimes defines the contract for launching agent packages.
package runtimes

import "context"

// Runtime tracks launches and registration. Close releases host resources;
// each implementation defines whether launched agents also stop with the host.
type Runtime interface {
	Spawn(ctx context.Context, requestID, reference string) (Result, error)
	Close() error
}

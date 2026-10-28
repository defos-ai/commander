package runtimes

import "context"

// Each runtime defines whether Close also stops its launched agents.
type Runtime interface {
	Spawn(ctx context.Context, requestID, reference string) (Result, error)
	Close() error
}

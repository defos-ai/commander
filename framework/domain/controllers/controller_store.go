package controllers

import (
	"context"
	"errors"
)

var (
	ErrUnknownController = errors.New("controller has not registered")
	ErrStaleObservation  = errors.New("controller heartbeat is not newer than the saved heartbeat")
)

// ControllerStore persists observations independently by controller ID. Methods
// support concurrent use and context cancellation. Upsert atomically rejects
// an equal or older LastHeartbeat with ErrStaleObservation, and clears Stale.
// Get returns ErrUnknownController when absent. List sorts by ID. Reads retain
// the reported status; callers use Snapshot to derive liveness at read time.
// Expired registrations remain visible until an explicit retention policy exists.
type ControllerStore interface {
	Upsert(ctx context.Context, record Controller) error
	Get(ctx context.Context, controllerID string) (Controller, error)
	List(ctx context.Context) ([]Controller, error)
}

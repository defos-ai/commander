package actions

import (
	"context"
)

// ActionStore holds catalog metadata learned from agent announcements.
// Executable implementations remain in Go;
// machine-specific readiness is reported in agent snapshots.
type ActionStore interface {
	Upsert(context.Context, Action) error
	Get(context.Context, string) (Action, error)
	List(context.Context) ([]Action, error)
}

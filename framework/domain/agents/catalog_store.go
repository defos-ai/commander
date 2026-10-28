package agents

import (
	"context"
	"errors"
)

var (
	ErrUnknownCatalogEntry   = errors.New("unknown agent catalog entry")
	ErrCatalogEntryExists    = errors.New("agent catalog entry already exists")
	ErrCatalogConflict       = errors.New("agent catalog entry changed")
	ErrCatalogWriteUncertain = errors.New("agent catalog write outcome is uncertain")
)

// CatalogStore holds package definitions separately from live agent inventory.
// The full canonical selector is the key; @stable and exact releases are distinct.
type CatalogStore interface {
	// Create atomically reserves an absent package at version 1 with equal initial
	// timestamps. Existing entries always return ErrCatalogEntryExists.
	Create(context.Context, CatalogEntry) error
	Get(context.Context, string) (CatalogEntry, error)
	// List returns independent entries sorted by package, with a nonnil empty slice.
	List(context.Context) ([]CatalogEntry, error)
	// Replace preserves the package and creation time, advances the expected version
	// by exactly one, and never moves update time backward. An absent entry returns
	// ErrUnknownCatalogEntry; stale versions return ErrCatalogConflict.
	// After ErrCatalogWriteUncertain, read saved state before another mutation.
	Replace(context.Context, CatalogEntry, uint64) error
}

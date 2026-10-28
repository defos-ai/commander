package agents

import (
	"context"
	"errors"
)

var (
	ErrUnknownAgentSource        = errors.New("unknown agent source")
	ErrAgentSourceExists         = errors.New("agent source already exists")
	ErrAgentSourceConflict       = errors.New("agent source changed")
	ErrAgentSourceWriteUncertain = errors.New("agent source write outcome is uncertain")
)

// AgentSourceStore holds source definitions separately from live agent inventory.
// The full canonical selector is the key; @stable and exact releases are distinct.
type AgentSourceStore interface {
	// Create atomically reserves an absent source at version 1 with equal initial
	// timestamps. Existing entries always return ErrAgentSourceExists.
	Create(context.Context, AgentSource) error
	Get(context.Context, string) (AgentSource, error)
	// List returns independent entries sorted by source, with a nonnil empty slice.
	List(context.Context) ([]AgentSource, error)
	// Replace preserves the source and creation time, advances the expected version
	// by exactly one, and never moves update time backward. An absent entry returns
	// ErrUnknownAgentSource; stale versions return ErrAgentSourceConflict.
	// After ErrAgentSourceWriteUncertain, read saved state before another mutation.
	Replace(context.Context, AgentSource, uint64) error
}

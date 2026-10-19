// Package launch defines the result shared by agent launch runtimes.
package launch

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/actions"
)

// Result reports an observed registration announcement. It does not promise
// controller persistence, readiness of every action, or future availability.
type Result struct {
	AgentID     string           `json:"agent_id"`
	AgentType   string           `json:"agent_type"`
	Package     string           `json:"package"`
	Runtime     string           `json:"runtime"`
	PID         int              `json:"pid,omitempty"`
	ContainerID string           `json:"container_id,omitempty"`
	Actions     []actions.Action `json:"actions"`
}

// Manager tracks launches and registration. Close releases host resources;
// each runtime defines whether launched agents also stop with the host.
type Manager interface {
	Spawn(ctx context.Context, requestID, reference string) (Result, error)
	Close() error
}

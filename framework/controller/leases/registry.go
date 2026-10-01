// Package leases manages controller-owned agent leases independently of storage
// and transport. A distributed store can implement the same conditional expiry.
package leases

import (
	"context"
	"errors"
	"time"

	agentmodel "commander/framework/domain/agents"
)

const (
	// DefaultTimeout allows one 30-second heartbeat interval plus 15 seconds of grace.
	DefaultTimeout       = agentmodel.DefaultHeartbeatTimeout
	DefaultSweepInterval = time.Second
)

// Store atomically compares the current heartbeat with the cutoff before deletion.
// A concurrent renewal must not be removed based on an older snapshot.
type Store interface {
	DeleteExpired(context.Context, time.Time) ([]agentmodel.Agent, error)
}

// Registry applies an agent lease policy to saved observations. The host
// schedules Sweep; heartbeat source times prevent delayed delivery from extending
// a lease. Agent and controller clocks must be synchronized.
type Registry struct {
	store   Store
	timeout time.Duration
}

func NewRegistry(store Store, timeout time.Duration) (*Registry, error) {
	if store == nil {
		return nil, errors.New("agent lease registry requires a store")
	}
	if timeout < 0 {
		return nil, errors.New("agent lease timeout must not be negative")
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Registry{store: store, timeout: timeout}, nil
}

// Sweep removes expired inventory records only; agent processes, artifacts and
// operation history belong to their hosts. It returns partial removals on error.
func (r *Registry) Sweep(ctx context.Context, now time.Time) ([]agentmodel.Agent, error) {
	return r.store.DeleteExpired(ctx, now.Add(-r.timeout))
}

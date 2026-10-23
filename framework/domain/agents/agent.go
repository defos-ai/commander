package agents

import (
	"bytes"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"slices"
	"time"
)

// DefaultHeartbeatTimeout allows a 30-second heartbeat interval plus 15 seconds of grace.
const DefaultHeartbeatTimeout = 45 * time.Second

// Agent is a received public snapshot with controller-owned liveness data.
// LastSeen records receipt time; ObservedAt orders source observations.
// LastHeartbeat is the source heartbeat time and alone renews the agent's lease.
// A zero LastHeartbeat has no lease. Stores retain the observed status.
type Agent struct {
	AgentSnapshot
	ObservedAt     time.Time `json:"observed_at,omitzero"`
	LastSeen       time.Time `json:"last_seen"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
	LeaseExpiresAt time.Time `json:"lease_expires_at,omitzero"`
	Stale          bool      `json:"stale"`
}

// Snapshot derives freshness from the persisted lease even when the controller
// is offline. Older records without a deadline use the default lease duration.
func (record Agent) Snapshot(now time.Time) Agent {
	record = record.Clone()
	expires := record.LeaseExpiresAt
	if expires.IsZero() {
		expires = record.LastHeartbeat.Add(DefaultHeartbeatTimeout)
	}
	record.Stale = record.Status != AgentStopped && (record.LastHeartbeat.IsZero() || !now.Before(expires))
	if record.Stale {
		record.Status = AgentUnavailable
	}
	if record.Stale || record.Status == AgentStopped {
		for i := range record.Sessions {
			if record.Sessions[i].State != "closed" && record.Sessions[i].State != "failed" {
				record.Sessions[i].State = "lost"
				record.Sessions[i].Reason = "Session owner is unavailable"
			}
		}
	}
	return record
}

func (record Agent) Clone() Agent {
	record.Sessions = sessions.CloneAll(record.Sessions)
	record.Work = record.Work.Clone()
	record.Responsibilities = slices.Clone(record.Responsibilities)
	if record.DiscoveryScope != nil {
		scope := *record.DiscoveryScope
		scope.Paths = slices.Clone(scope.Paths)
		scope.Services = slices.Clone(scope.Services)
		record.DiscoveryScope = &scope
	}
	record.Config = bytes.Clone(record.Config)
	record.Actions = slices.Clone(record.Actions)
	for i := range record.Actions {
		record.Actions[i] = record.Actions[i].Clone()
	}
	return record
}

// Package controllers describes public controller registrations and their leases.
package controllers

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"commander/framework/id"
)

type Status string

const (
	Starting    Status = "starting"
	Ready       Status = "ready"
	Degraded    Status = "degraded"
	Draining    Status = "draining"
	Stopped     Status = "stopped"
	Unavailable Status = "unavailable"
)

// ServerInfo contains host metadata only; unknown fields may be empty.
type ServerInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

// Controller is a public runtime registration. LastHeartbeat orders updates;
// LeaseExpiresAt determines freshness without depending on the writer running.
type Controller struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	Status         Status     `json:"status"`
	StatusReason   string     `json:"status_reason,omitempty"`
	Server         ServerInfo `json:"server"`
	Version        string     `json:"version"`
	AgentSelector  string     `json:"agent_selector"`
	StartedAt      time.Time  `json:"started_at"`
	LastHeartbeat  time.Time  `json:"last_heartbeat"`
	LeaseExpiresAt time.Time  `json:"lease_expires_at"`
	Stale          bool       `json:"stale"`
}

// Snapshot derives liveness without mutating the stored observation. A cleanly
// stopped controller stays stopped; an expired or missing lease is unavailable.
func (record Controller) Snapshot(now time.Time) Controller {
	record.Stale = record.Status != Stopped && (record.LastHeartbeat.IsZero() || record.LeaseExpiresAt.IsZero() || !now.Before(record.LeaseExpiresAt))
	if record.Stale {
		record.Status = Unavailable
	}
	return record
}

func (record Controller) Validate() error {
	runtimeType, ok := id.RuntimeType(record.ID, id.Controller)
	if !ok || runtimeType != record.Type {
		return errors.New("controller ID must identify its controller type")
	}
	switch record.Status {
	case Starting, Ready, Degraded, Draining, Stopped, Unavailable:
	default:
		return errors.New("invalid controller status")
	}
	for _, value := range []time.Time{record.StartedAt, record.LastHeartbeat, record.LeaseExpiresAt} {
		if value.IsZero() || value.Year() < 1 || value.Year() > 9999 {
			return errors.New("controller timestamps must be nonzero and within years 1 through 9999")
		}
	}
	if record.LastHeartbeat.Before(record.StartedAt) {
		return errors.New("controller heartbeat must not precede its start time")
	}
	if record.LeaseExpiresAt.Before(record.LastHeartbeat) || record.Status != Stopped && record.LeaseExpiresAt.Equal(record.LastHeartbeat) {
		return errors.New("controller lease must follow its heartbeat, or equal it when stopped")
	}
	for _, value := range []string{record.StatusReason, record.Version, record.AgentSelector, record.Server.Hostname, record.Server.OS, record.Server.Arch} {
		if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return errors.New("controller metadata must contain valid UTF-8 without control characters")
		}
	}
	for _, value := range []string{record.Server.Hostname, record.Server.OS, record.Server.Arch} {
		if strings.ContainsAny(value, `/\`) || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
			return errors.New("controller server metadata must not contain whitespace or path separators")
		}
	}
	return nil
}

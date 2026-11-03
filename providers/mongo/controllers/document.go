package controllers

import (
	"fmt"
	"time"

	controllermodel "github.com/defos-ai/commander/framework/domain/controllers"
	"github.com/defos-ai/commander/providers/mongo/internal/bsonvalue"
)

type controllerDocument struct {
	ID             string                     `bson:"_id"`
	Type           string                     `bson:"type"`
	Status         controllermodel.Status     `bson:"status"`
	StatusReason   string                     `bson:"status_reason,omitempty"`
	Server         controllermodel.ServerInfo `bson:"server"`
	Version        string                     `bson:"version"`
	AgentSelector  string                     `bson:"agent_selector"`
	StartedAt      bsonvalue.Timestamp        `bson:"started_at"`
	LastHeartbeat  bsonvalue.Timestamp        `bson:"last_heartbeat"`
	LeaseExpiresAt bsonvalue.Timestamp        `bson:"lease_expires_at"`
	Stale          bool                       `bson:"stale"`
}

func encode(record controllermodel.Controller) controllerDocument {
	return controllerDocument{
		ID: record.ID, Type: record.Type, Status: record.Status, StatusReason: record.StatusReason,
		Server: record.Server, Version: record.Version, AgentSelector: record.AgentSelector,
		StartedAt: bsonvalue.NewTimestamp(record.StartedAt), LastHeartbeat: bsonvalue.NewTimestamp(record.LastHeartbeat),
		LeaseExpiresAt: bsonvalue.NewTimestamp(record.LeaseExpiresAt),
	}
}

func (document controllerDocument) decode() (controllermodel.Controller, error) {
	record := controllermodel.Controller{
		ID: document.ID, Type: document.Type, Status: document.Status, StatusReason: document.StatusReason,
		Server: document.Server, Version: document.Version, AgentSelector: document.AgentSelector, Stale: document.Stale,
	}
	for _, field := range []struct {
		name  string
		value bsonvalue.Timestamp
		out   *time.Time
	}{
		{"started_at", document.StartedAt, &record.StartedAt},
		{"last_heartbeat", document.LastHeartbeat, &record.LastHeartbeat},
		{"lease_expires_at", document.LeaseExpiresAt, &record.LeaseExpiresAt},
	} {
		value, err := field.value.Decode()
		if err != nil {
			return controllermodel.Controller{}, fmt.Errorf("invalid controller %q %s: %w", document.ID, field.name, err)
		}
		*field.out = value
	}
	if err := record.Validate(); err != nil {
		return controllermodel.Controller{}, fmt.Errorf("invalid stored controller %q: %w", document.ID, err)
	}
	return record, nil
}

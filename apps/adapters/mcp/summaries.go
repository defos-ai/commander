package missionsmcp

import (
	"encoding/json"
	"time"

	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/operations"
)

// Listings carry selection and progress information. Schemas, configuration and
// evidence stay in detail queries, rather than repeating on every inventory read.
type actionSummary struct {
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	RequiredPermissions []string `json:"required_permissions"`
}

type agentActionSummary struct {
	Name                string                `json:"name"`
	Status              actions.Status        `json:"status"`
	StatusReason        string                `json:"status_reason,omitempty"`
	RequiredPermissions []string              `json:"required_permissions"`
	MissingRequirements []actions.Requirement `json:"missing_requirements,omitempty"`
}

type agentSummary struct {
	ID                 string               `json:"id"`
	Type               agents.AgentType     `json:"type"`
	Status             agents.AgentStatus   `json:"status"`
	Server             agents.ServerInfo    `json:"server"`
	Responsibilities   []string             `json:"responsibilities,omitempty"`
	Actions            []agentActionSummary `json:"actions"`
	GrantedPermissions []string             `json:"granted_permissions"`
	LastSeen           time.Time            `json:"last_seen"`
	LastHeartbeat      time.Time            `json:"last_heartbeat"`
	LeaseExpiresAt     time.Time            `json:"lease_expires_at,omitzero"`
	Stale              bool                 `json:"stale"`
}

func summarizeAgent(agent agents.Agent) agentSummary {
	items := make([]agentActionSummary, 0, len(agent.Actions))
	for _, action := range agent.Actions {
		items = append(items, agentActionSummary{
			Name: action.Name, Status: action.Status, StatusReason: action.StatusReason,
			RequiredPermissions: action.RequiredPermissions,
			MissingRequirements: action.MissingRequirements,
		})
	}
	return agentSummary{
		ID: agent.ID, Type: agent.Type, Status: agent.Status, Server: agent.Server,
		Responsibilities: agent.Responsibilities, Actions: items, GrantedPermissions: agent.GrantedPermissions,
		LastSeen: agent.LastSeen, LastHeartbeat: agent.LastHeartbeat, LeaseExpiresAt: agent.LeaseExpiresAt, Stale: agent.Stale,
	}
}

type missionSummary struct {
	Name string `json:"name"`
}

type operationSummary struct {
	ID                 string                        `json:"id"`
	Mission            missionSummary                `json:"mission"`
	Input              json.RawMessage               `json:"input"`
	Status             operations.Status             `json:"status"`
	Preflight          *operations.Preflight         `json:"preflight,omitempty"`
	TaskCounts         map[operations.TaskStatus]int `json:"task_counts"`
	PendingDecisions   int                           `json:"pending_decisions"`
	PendingQuestions   int                           `json:"pending_questions"`
	LastQuestion       *operations.Question          `json:"last_question"`
	RemainingQuestions int                           `json:"remaining_questions"`
	ReportID           string                        `json:"report_id,omitempty"`
	Error              string                        `json:"error,omitempty"`
	Version            uint64                        `json:"version"`
	CreatedAt          time.Time                     `json:"created_at"`
	UpdatedAt          time.Time                     `json:"updated_at"`
}

func summarizeOperation(operation operations.Operation) operationSummary {
	summary := operationSummary{
		ID: operation.ID, Mission: missionSummary{Name: operation.Mission.Name}, Input: operation.Input,
		Status: operation.Status, TaskCounts: make(map[operations.TaskStatus]int),
		Preflight: operation.Preflight,
		ReportID:  operation.ReportID, Error: operation.Error, Version: operation.Version,
		CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt,
	}
	for _, task := range operation.Tasks {
		summary.TaskCounts[task.Status]++
		if c := task.Continuation; c != nil && c.Status == operations.ContinuationPending && c.DecisionBy == operations.DecisionModel {
			summary.PendingDecisions++
		}
	}
	summary.LastQuestion, summary.RemainingQuestions = pendingQuestions(operation, time.Now())
	if summary.LastQuestion != nil {
		summary.PendingQuestions = summary.RemainingQuestions + 1
	}
	return summary
}

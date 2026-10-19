package missionsmcp

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

// Listings carry selection and progress information. Schemas, configuration and
// evidence stay in detail queries, rather than repeating on every inventory read.
type actionSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type agentActionSummary struct {
	Name                string                `json:"name"`
	Status              actions.Status        `json:"status"`
	StatusReason        string                `json:"status_reason,omitempty"`
	MissingRequirements []actions.Requirement `json:"missing_requirements,omitempty"`
	RequiredSecrets     []string              `json:"required_secrets,omitempty"`
}

type agentSummary struct {
	ID               string               `json:"id"`
	Type             agents.AgentType     `json:"type"`
	Status           agents.AgentStatus   `json:"status"`
	Work             *agents.Work         `json:"work,omitempty"`
	Server           agents.ServerInfo    `json:"server"`
	Responsibilities []string             `json:"responsibilities,omitempty"`
	Actions          []agentActionSummary `json:"actions"`
	LastSeen         time.Time            `json:"last_seen"`
	LastHeartbeat    time.Time            `json:"last_heartbeat"`
	LeaseExpiresAt   time.Time            `json:"lease_expires_at,omitzero"`
	Stale            bool                 `json:"stale"`
}

func summarizeAgent(agent agents.Agent) agentSummary {
	items := make([]agentActionSummary, 0, len(agent.Actions))
	for _, action := range agent.Actions {
		items = append(items, agentActionSummary{
			Name: action.Name, Status: action.Status, StatusReason: action.StatusReason,
			MissingRequirements: action.MissingRequirements,
			RequiredSecrets:     slices.Clone(action.RequiredSecrets),
		})
	}
	return agentSummary{
		ID: agent.ID, Type: agent.Type, Status: agent.Status, Work: agent.Work, Server: agent.Server,
		Responsibilities: agent.Responsibilities, Actions: items,
		LastSeen: agent.LastSeen, LastHeartbeat: agent.LastHeartbeat, LeaseExpiresAt: agent.LeaseExpiresAt, Stale: agent.Stale,
	}
}

type missionSummary struct {
	Name string `json:"name"`
}

type continuationSummary struct {
	Status     operations.ContinuationStatus `json:"status"`
	DecisionBy operations.DecisionBy         `json:"decision_by"`
	Version    uint64                        `json:"version"`
}

type taskSummary struct {
	ID                  string                `json:"id"`
	ParentTaskID        string                `json:"parent_task_id,omitempty"`
	StepID              string                `json:"step_id,omitempty"`
	AgentID             string                `json:"agent_id"`
	AgentType           string                `json:"agent_type,omitempty"`
	MissingRequirements []actions.Requirement `json:"missing_requirements,omitempty"`
	ActionName          string                `json:"action_name"`
	DeadlineAt          *time.Time            `json:"deadline_at,omitempty"`
	Status              operations.TaskStatus `json:"status"`
	Continuation        *continuationSummary  `json:"continuation,omitempty"`
	Error               string                `json:"error,omitempty"`
	StartedAt           time.Time             `json:"started_at"`
	FinishedAt          *time.Time            `json:"finished_at,omitempty"`
}

func summarizeTask(task operations.Task, record agents.Agent, now time.Time) taskSummary {
	summary := taskSummary{
		ID: task.ID, ParentTaskID: task.ParentTaskID, StepID: task.StepID,
		AgentID: task.AgentID, ActionName: task.ActionName, Status: task.Status,
		DeadlineAt: task.DeadlineAt,
		Error:      task.Error, StartedAt: task.StartedAt, FinishedAt: task.FinishedAt,
	}
	if requirements := operations.AssessTaskRequirements(task, record, now); len(requirements) != 0 {
		summary.AgentType = string(record.Type)
		summary.MissingRequirements = requirements
	}
	if c := task.Continuation; c != nil {
		summary.Continuation = &continuationSummary{Status: c.Status, DecisionBy: c.DecisionBy, Version: c.Version}
	}
	return summary
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
	CreatedAt          time.Time                     `json:"created_at"`
	UpdatedAt          time.Time                     `json:"updated_at"`
}

func summarizeOperation(operation operations.Operation) operationSummary {
	summary := operationSummary{
		ID: operation.ID, Mission: missionSummary{Name: operation.Mission.Name}, Input: operation.Input,
		Status: operation.Status, TaskCounts: make(map[operations.TaskStatus]int),
		Preflight: operation.Preflight,
		ReportID:  operation.ReportID, Error: operation.Error,
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

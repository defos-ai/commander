package models

import (
	"encoding/json"
	"time"
)

type StartOperationRequest struct {
	OperationID string          `json:"operation_id,omitempty"`
	MissionName string          `json:"mission_name"`
	Input       json.RawMessage `json:"input"`
}

type CancelOperationRequest struct{}

type CompleteOperationRequest struct {
	Output json.RawMessage `json:"output"`
}

type ProposeCompletionRequest struct {
	Output json.RawMessage `json:"output"`
}

type CompletionProposalResponse struct {
	ID         string          `json:"id"`
	Output     json.RawMessage `json:"output"`
	ProposedBy string          `json:"proposed_by"`
	ProposedAt time.Time       `json:"proposed_at"`
}

type FailOperationRequest struct {
	Reason string `json:"reason"`
}

type OperationResponse struct {
	ID                 string                      `json:"id"`
	Mission            MissionResponse             `json:"mission"`
	Input              json.RawMessage             `json:"input"`
	Status             string                      `json:"status"`
	Progress           OperationProgressResponse   `json:"progress"`
	Preflight          *PreflightResponse          `json:"preflight,omitempty"`
	Affinity           *AffinityResponse           `json:"affinity,omitempty"`
	Tasks              []TaskResponse              `json:"tasks"`
	Questions          []QuestionResponse          `json:"questions"`
	Output             json.RawMessage             `json:"output,omitempty"`
	ReportID           string                      `json:"report_id,omitempty"`
	CompletedBy        string                      `json:"completed_by,omitempty"`
	CompletionProposal *CompletionProposalResponse `json:"completion_proposal,omitempty"`
	Error              string                      `json:"error,omitempty"`
	Events             []EventResponse             `json:"events"`
	CreatedAt          time.Time                   `json:"created_at"`
	UpdatedAt          time.Time                   `json:"updated_at"`
}

type OperationProgressResponse struct {
	TaskCounts       map[string]int `json:"task_counts"`
	TotalTasks       int            `json:"total_tasks"`
	FinishedTasks    int            `json:"finished_tasks"`
	PendingDecisions int            `json:"pending_decisions"`
	PendingQuestions int            `json:"pending_questions"`
	Settled          bool           `json:"settled"`
}

type AffinityResponse struct {
	ForActions []string `json:"for_actions"`
	AgentID    string   `json:"agent_id,omitempty"`
	Error      string   `json:"error,omitempty"`
}

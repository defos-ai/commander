package missionsmcp

import (
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
)

type operationProgress struct {
	TaskCounts       map[operations.TaskStatus]int `json:"task_counts"`
	TotalTasks       int                           `json:"total_tasks"`
	FinishedTasks    int                           `json:"finished_tasks"`
	PendingDecisions int                           `json:"pending_decisions"`
	PendingQuestions int                           `json:"pending_questions"`
	Settled          bool                          `json:"settled"`
}

func describeProgress(progress operations.Progress) operationProgress {
	counts := make(map[operations.TaskStatus]int, len(progress.TaskCounts))
	for status, count := range progress.TaskCounts {
		counts[status] = count
	}
	return operationProgress{
		TaskCounts: counts, TotalTasks: progress.TotalTasks, FinishedTasks: progress.FinishedTasks,
		PendingDecisions: progress.PendingDecisions, PendingQuestions: progress.PendingQuestions,
		Settled: progress.Settled,
	}
}

type operationResult struct {
	ID        string                `json:"id"`
	Status    operations.Status     `json:"status"`
	Progress  operationProgress     `json:"progress"`
	Preflight *operations.Preflight `json:"preflight,omitempty"`
	ReportID  string                `json:"report_id,omitempty"`
	Error     string                `json:"error,omitempty"`
	Message   string                `json:"message,omitempty"`
}

func commandOperation(operation operations.Operation, err error) (operationResult, error) {
	return operationResult{
		ID: operation.ID, Status: operation.Status,
		Progress:  describeProgress(operation.AssessProgress(time.Now().UTC())),
		Preflight: operation.Preflight, ReportID: operation.ReportID, Error: operation.Error,
	}, err
}

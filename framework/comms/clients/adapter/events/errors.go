package events

import (
	"context"
	"encoding/json"
	"errors"

	"commander/framework/domain"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/executions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
)

type remoteError struct {
	Message        string                     `json:"message"`
	Code           string                     `json:"code,omitempty"`
	CommandFailure *executions.CommandFailure `json:"command_failure,omitempty"`
}

func (e *remoteError) Error() string { return e.Message }

func (e *remoteError) Unwrap() error {
	if e.CommandFailure != nil {
		return e.CommandFailure
	}
	for _, entry := range errorCodes {
		if entry.code == e.Code {
			return entry.target
		}
	}
	return nil
}

// Order defines wire classification precedence for errors with multiple causes.
// An unknown execution outcome must take precedence over the timeout causing it.
var errorCodes = []struct {
	code   string
	target error
}{
	{"indeterminate", executions.ErrIndeterminate},
	{"operation_write_uncertain", operations.ErrWriteUncertain},
	{"canceled", context.Canceled},
	{"deadline_exceeded", context.DeadlineExceeded},
	{"controller_closed", domain.ErrControllerClosed},
	{"unknown_agent", agentmodel.ErrUnknownAgent},
	{"agent_not_running", agentmodel.ErrAgentNotRunning},
	{"unknown_artifact", artifacts.ErrUnknownArtifact},
	{"artifact_conflict", artifacts.ErrArtifactConflict},
	{"unknown_mission", missions.ErrUnknownMission},
	{"unknown_operation", operations.ErrUnknownOperation},
	{"operation_conflict", operations.ErrOperationConflict},
	{"unknown_report", reports.ErrUnknownReport},
	{"runner_closed", operations.ErrClosed},
	{"operation_finished", operations.ErrFinished},
	{"question_pending", operations.ErrQuestionPending},
	{"task_not_running", operations.ErrNotRunning},
	{"invalid_input", domain.ErrInvalidInput},
	{"unknown_task", operations.ErrUnknownTask},
	{"unknown_question", operations.ErrUnknownQuestion},
	{"question_answered", operations.ErrQuestionAnswered},
	{"question_expired", operations.ErrQuestionExpired},
	{"continuation_conflict", operations.ErrContinuationConflict},
	{"task_limit_exceeded", operations.ErrTaskLimitExceeded},
	{"operation_active_tasks", operations.ErrActiveTasks},
	{"operation_unresolved_work", operations.ErrUnresolvedWork},
	{"invalid_output", domain.ErrInvalidOutput},
}

func EncodeError(err error) *ControllerError {
	result := &ControllerError{Message: err.Error()}
	for _, entry := range errorCodes {
		if errors.Is(err, entry.target) {
			result.Code = entry.code
			break
		}
	}
	var failure *executions.CommandFailure
	if errors.As(err, &failure) {
		result.Code = "command_failure"
		result.Details, _ = json.Marshal(failure)
	}
	return result
}

func DecodeError(wire *ControllerError) error {
	result := &remoteError{Message: wire.Message, Code: wire.Code}
	if wire.Code == "command_failure" {
		var failure executions.CommandFailure
		if json.Unmarshal(wire.Details, &failure) == nil {
			result.CommandFailure = &failure
		}
	}
	return result
}

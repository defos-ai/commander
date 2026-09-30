package operations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
)

type operationMetadata struct {
	ID          string                `json:"id"`
	Mission     missions.Mission      `json:"mission"`
	Input       json.RawMessage       `json:"input"`
	Status      operations.Status     `json:"status"`
	Preflight   *operations.Preflight `json:"preflight,omitempty"`
	TaskIDs     []string              `json:"task_ids"`
	QuestionIDs []string              `json:"question_ids"`
	Output      json.RawMessage       `json:"output,omitempty"`
	ReportID    string                `json:"report_id,omitempty"`
	Error       string                `json:"error,omitempty"`
	Events      []operations.Event    `json:"events"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

func encodeOperation(op operations.Operation) (map[string]json.RawMessage, error) {
	metadata := operationMetadata{ID: op.ID, Mission: op.Mission, Input: op.Input,
		Status: op.Status, Preflight: op.Preflight, Output: op.Output, ReportID: op.ReportID,
		Error: op.Error, Events: op.Events, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt,
		TaskIDs: make([]string, 0, len(op.Tasks)), QuestionIDs: make([]string, 0, len(op.Questions))}
	records := make(map[string]json.RawMessage, 1+len(op.Tasks)+len(op.Questions))
	for _, task := range op.Tasks {
		if err := addRecord(records, "tasks/", task.ID, task); err != nil {
			return nil, err
		}
		metadata.TaskIDs = append(metadata.TaskIDs, task.ID)
	}
	for _, question := range op.Questions {
		if err := addRecord(records, "questions/", question.ID, question); err != nil {
			return nil, err
		}
		metadata.QuestionIDs = append(metadata.QuestionIDs, question.ID)
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode operation: %w", err)
	}
	records["operation"] = data
	return records, nil
}

func addRecord(records map[string]json.RawMessage, prefix, recordID string, value any) error {
	key := prefix + recordID
	if !id.Valid(recordID) || records[key] != nil {
		return fmt.Errorf("invalid or duplicate operation record %q", key)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", key, err)
	}
	records[key] = data
	return nil
}

func decodeOperation(records map[string]json.RawMessage, operationID string) (operations.Operation, error) {
	if len(records) == 0 {
		return operations.Operation{}, operations.ErrUnknownOperation
	}
	var metadata operationMetadata
	if err := decodeRecord(records, "operation", &metadata); err != nil {
		return operations.Operation{}, err
	}
	if metadata.ID != operationID || metadata.TaskIDs == nil || metadata.QuestionIDs == nil {
		return operations.Operation{}, errors.New("operation metadata requires its matching UUID and task/question indexes")
	}
	if len(records) != 1+len(metadata.TaskIDs)+len(metadata.QuestionIDs) {
		return operations.Operation{}, errors.New("operation records do not match their metadata indexes")
	}
	op := operations.Operation{ID: metadata.ID, Mission: metadata.Mission, Input: metadata.Input,
		Status: metadata.Status, Preflight: metadata.Preflight, Output: metadata.Output, ReportID: metadata.ReportID,
		Error: metadata.Error, Events: metadata.Events, CreatedAt: metadata.CreatedAt, UpdatedAt: metadata.UpdatedAt,
		Tasks: make([]operations.Task, 0, len(metadata.TaskIDs)), Questions: make([]operations.Question, 0, len(metadata.QuestionIDs))}
	seen := make(map[string]bool, len(records))
	for _, taskID := range metadata.TaskIDs {
		key := "tasks/" + taskID
		var task operations.Task
		if err := decodeIndexedRecord(records, key, taskID, seen, &task); err != nil {
			return operations.Operation{}, err
		}
		if task.ID != taskID {
			return operations.Operation{}, fmt.Errorf("task %s has a mismatched identity", taskID)
		}
		op.Tasks = append(op.Tasks, task)
	}
	for _, questionID := range metadata.QuestionIDs {
		key := "questions/" + questionID
		var question operations.Question
		if err := decodeIndexedRecord(records, key, questionID, seen, &question); err != nil {
			return operations.Operation{}, err
		}
		if question.ID != questionID {
			return operations.Operation{}, fmt.Errorf("question %s has a mismatched identity", questionID)
		}
		op.Questions = append(op.Questions, question)
	}
	return op.Clone(), nil
}

func decodeIndexedRecord(records map[string]json.RawMessage, key, recordID string, seen map[string]bool, target any) error {
	if !id.Valid(recordID) || seen[key] {
		return fmt.Errorf("invalid or duplicate operation record %q", key)
	}
	seen[key] = true
	return decodeRecord(records, key, target)
}

func decodeRecord(records map[string]json.RawMessage, key string, target any) error {
	data, exists := records[key]
	if !exists {
		return fmt.Errorf("operation record %q is missing", key)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", key, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("record %q must contain exactly one JSON value", key)
	}
	return nil
}

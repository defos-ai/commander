package operations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

type operationMetadata struct {
	ID              string                      `json:"id"`
	Mission         missions.Mission            `json:"mission"`
	Input           json.RawMessage             `json:"input"`
	Status          operations.Status           `json:"status"`
	Preflight       *operations.Preflight       `json:"preflight,omitempty"`
	TaskIDs         []string                    `json:"task_ids"`
	QuestionIDs     []string                    `json:"question_ids"`
	Output          json.RawMessage             `json:"output,omitempty"`
	ReportID        string                      `json:"report_id,omitempty"`
	CompletedBy     operations.CompletionOrigin `json:"completed_by,omitempty"`
	CompletionDraft json.RawMessage             `json:"completion_draft,omitempty"`
	Error           string                      `json:"error,omitempty"`
	EventSequences  []uint64                    `json:"event_sequences"`
	CreatedAt       time.Time                   `json:"created_at"`
	UpdatedAt       time.Time                   `json:"updated_at"`
}

func encodeOperation(op operations.Operation) (map[string]json.RawMessage, error) {
	metadata := operationMetadata{ID: op.ID, Mission: op.Mission, Input: op.Input,
		Status: op.Status, Preflight: op.Preflight, Output: op.Output, ReportID: op.ReportID, CompletedBy: op.CompletedBy, CompletionDraft: op.CompletionDraft,
		Error: op.Error, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt,
		TaskIDs: make([]string, 0, len(op.Tasks)), QuestionIDs: make([]string, 0, len(op.Questions)),
		EventSequences: make([]uint64, 0, len(op.Events))}
	records := make(map[string]json.RawMessage, 1+len(op.Tasks)+len(op.Questions)+len(op.Events))
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
	for _, event := range op.Events {
		key := eventKey(event.Sequence)
		if event.Sequence == 0 || records[key] != nil || event.OperationID != "" && event.OperationID != op.ID {
			return nil, fmt.Errorf("invalid or duplicate operation event %q", key)
		}
		data, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		records[key] = data
		metadata.EventSequences = append(metadata.EventSequences, event.Sequence)
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

type operationReader interface {
	Get(string) (json.RawMessage, error)
	Has(string) bool
	Len() int
}

type operationRecords map[string]json.RawMessage

func (r operationRecords) Get(key string) (json.RawMessage, error) {
	data, exists := r[key]
	if !exists {
		return nil, fmt.Errorf("operation record %q is missing", key)
	}
	return data, nil
}

func (r operationRecords) Has(key string) bool { _, exists := r[key]; return exists }
func (r operationRecords) Len() int            { return len(r) }

func readOperationMetadata(records operationReader, operationID string) (operationMetadata, error) {
	if records.Len() == 0 {
		return operationMetadata{}, operations.ErrUnknownOperation
	}
	var metadata operationMetadata
	if err := decodeRecord(records, "operation", &metadata); err != nil {
		return operationMetadata{}, err
	}
	if metadata.ID != operationID || metadata.TaskIDs == nil || metadata.QuestionIDs == nil || metadata.EventSequences == nil {
		return operationMetadata{}, errors.New("operation metadata requires its matching UUID and task/question/event indexes")
	}
	if records.Len() != 1+len(metadata.TaskIDs)+len(metadata.QuestionIDs)+len(metadata.EventSequences) {
		return operationMetadata{}, errors.New("operation records do not match their metadata indexes")
	}
	seen := make(map[string]bool, records.Len())
	for _, index := range []struct {
		prefix string
		ids    []string
	}{{"tasks/", metadata.TaskIDs}, {"questions/", metadata.QuestionIDs}} {
		for _, recordID := range index.ids {
			key := index.prefix + recordID
			if !id.Valid(recordID) || seen[key] || !records.Has(key) {
				return operationMetadata{}, fmt.Errorf("invalid, missing or duplicate operation record %q", key)
			}
			seen[key] = true
		}
	}
	for _, sequence := range metadata.EventSequences {
		key := eventKey(sequence)
		if sequence == 0 || seen[key] || !records.Has(key) {
			return operationMetadata{}, fmt.Errorf("invalid, missing or duplicate event record %q", key)
		}
		seen[key] = true
	}
	return metadata, nil
}

func decodeOperation(data map[string]json.RawMessage, operationID string) (operations.Operation, error) {
	if len(data) == 0 {
		return operations.Operation{}, operations.ErrUnknownOperation
	}
	records := operationRecords(data)
	metadata, err := readOperationMetadata(records, operationID)
	if err != nil {
		return operations.Operation{}, err
	}
	op := operations.Operation{ID: metadata.ID, Mission: metadata.Mission, Input: metadata.Input,
		Status: metadata.Status, Preflight: metadata.Preflight, Output: metadata.Output, ReportID: metadata.ReportID, CompletedBy: metadata.CompletedBy, CompletionDraft: metadata.CompletionDraft,
		Error: metadata.Error, CreatedAt: metadata.CreatedAt, UpdatedAt: metadata.UpdatedAt,
		Tasks: make([]operations.Task, 0, len(metadata.TaskIDs)), Questions: make([]operations.Question, 0, len(metadata.QuestionIDs))}
	for _, taskID := range metadata.TaskIDs {
		task, err := readTask(records, taskID)
		if err != nil {
			return operations.Operation{}, err
		}
		op.Tasks = append(op.Tasks, task)
	}
	for _, questionID := range metadata.QuestionIDs {
		key := "questions/" + questionID
		var question operations.Question
		if err := decodeRecord(records, key, &question); err != nil {
			return operations.Operation{}, err
		}
		if question.ID != questionID {
			return operations.Operation{}, fmt.Errorf("question %s has a mismatched identity", questionID)
		}
		op.Questions = append(op.Questions, question)
	}
	for _, sequence := range metadata.EventSequences {
		event, err := readEvent(records, operationID, sequence)
		if err != nil {
			return operations.Operation{}, err
		}
		op.Events = append(op.Events, event)
	}
	return op.Clone(), nil
}

func readTask(records operationReader, taskID string) (operations.Task, error) {
	var task operations.Task
	if err := decodeRecord(records, "tasks/"+taskID, &task); err != nil {
		return operations.Task{}, err
	}
	if task.ID != taskID {
		return operations.Task{}, fmt.Errorf("task %s has a mismatched identity", taskID)
	}
	return task, nil
}

func eventKey(sequence uint64) string { return "events/" + strconv.FormatUint(sequence, 10) }

func readEvent(records operationReader, operationID string, sequence uint64) (operations.Event, error) {
	var event operations.Event
	if err := decodeRecord(records, eventKey(sequence), &event); err != nil {
		return operations.Event{}, err
	}
	if event.Sequence != sequence || event.OperationID != "" && event.OperationID != operationID {
		return operations.Event{}, fmt.Errorf("event %d has a mismatched identity", sequence)
	}
	return event, nil
}

func decodeRecord(records operationReader, key string, target any) error {
	data, err := records.Get(key)
	if err != nil {
		return err
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

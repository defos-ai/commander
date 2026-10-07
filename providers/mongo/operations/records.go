package operations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

const (
	collection          = "operations"
	tasksCollection     = "operation_tasks"
	questionsCollection = "operation_questions"
	eventsCollection    = "operation_events"
)

// JSON bodies preserve nanosecond timestamps and unsigned decision versions.
// Indexed fields stay separate; the operation body has no children.
type operationDocument struct {
	ID            string            `bson:"_id"`
	Revision      int64             `bson:"revision"`
	Status        operations.Status `bson:"status"`
	TaskCount     int64             `bson:"task_count"`
	QuestionCount int64             `bson:"question_count"`
	EventCount    int64             `bson:"event_count"`
	Data          json.RawMessage   `bson:"data"`
}

type recordDocument struct {
	ID          string          `bson:"_id"`
	OperationID string          `bson:"operation_id"`
	Key         string          `bson:"key"`
	Position    int64           `bson:"position"`
	Data        json.RawMessage `bson:"data"`
}

type operationRecords struct {
	metadata  operationDocument
	tasks     []recordDocument
	questions []recordDocument
	events    []recordDocument
}

func encodeOperation(op operations.Operation) (operationRecords, error) {
	records := operationRecords{metadata: operationDocument{ID: op.ID, Status: op.Status,
		TaskCount: int64(len(op.Tasks)), QuestionCount: int64(len(op.Questions)), EventCount: int64(len(op.Events))}}
	var err error
	for _, group := range []struct {
		length int
		at     func(int) (string, any, error)
		out    *[]recordDocument
	}{
		{len(op.Tasks), func(i int) (string, any, error) { return op.Tasks[i].ID, op.Tasks[i], validateRecordID(op.Tasks[i].ID) }, &records.tasks},
		{len(op.Questions), func(i int) (string, any, error) {
			return op.Questions[i].ID, op.Questions[i], validateRecordID(op.Questions[i].ID)
		}, &records.questions},
		{len(op.Events), func(i int) (string, any, error) {
			event := op.Events[i]
			if event.Sequence == 0 || event.OperationID != "" && event.OperationID != op.ID {
				return "", nil, errors.New("invalid operation event identity")
			}
			return eventKey(event.Sequence), event, nil
		}, &records.events},
	} {
		seen := make(map[string]bool, group.length)
		for i := range group.length {
			key, value, err := group.at(i)
			if err != nil {
				return operationRecords{}, err
			}
			if seen[key] {
				return operationRecords{}, fmt.Errorf("duplicate operation record %q", key)
			}
			seen[key] = true
			data, err := json.Marshal(value)
			if err != nil {
				return operationRecords{}, err
			}
			*group.out = append(*group.out, recordDocument{ID: recordID(op.ID, key), OperationID: op.ID, Key: key, Position: int64(i + 1), Data: data})
		}
	}
	op.Tasks, op.Questions, op.Events = nil, nil, nil
	records.metadata.Data, err = json.Marshal(op)
	return records, err
}

func (d operationDocument) decode() (operations.Operation, error) {
	var op operations.Operation
	if err := decodeRecord(d.Data, &op); err != nil {
		return op, err
	}
	if !id.Valid(d.ID) || op.ID != d.ID || op.Status != d.Status || d.Revision < 1 ||
		d.TaskCount < 0 || d.QuestionCount < 0 || d.EventCount < 0 ||
		len(op.Tasks) != 0 || len(op.Questions) != 0 || len(op.Events) != 0 {
		return operations.Operation{}, errors.New("invalid operation metadata")
	}
	return op, nil
}

func (r operationRecords) decode() (operations.Operation, error) {
	op, err := r.metadata.decode()
	if err != nil {
		return op, err
	}
	if int64(len(r.tasks)) != r.metadata.TaskCount || int64(len(r.questions)) != r.metadata.QuestionCount || int64(len(r.events)) != r.metadata.EventCount {
		return operations.Operation{}, errors.New("operation records do not match their metadata counts")
	}
	for i, doc := range r.tasks {
		if err := doc.validatePosition(op.ID, int64(i+1)); err != nil {
			return operations.Operation{}, err
		}
		task, err := doc.task()
		if err != nil {
			return operations.Operation{}, err
		}
		op.Tasks = append(op.Tasks, task)
	}
	for i, doc := range r.questions {
		if err := doc.validatePosition(op.ID, int64(i+1)); err != nil {
			return operations.Operation{}, err
		}
		var question operations.Question
		if err := decodeRecord(doc.Data, &question); err != nil {
			return operations.Operation{}, err
		}
		if !id.Valid(doc.Key) || question.ID != doc.Key {
			return operations.Operation{}, errors.New("question identity does not match its record")
		}
		op.Questions = append(op.Questions, question)
	}
	for i, doc := range r.events {
		if err := doc.validatePosition(op.ID, int64(i+1)); err != nil {
			return operations.Operation{}, err
		}
		event, err := doc.event()
		if err != nil {
			return operations.Operation{}, err
		}
		op.Events = append(op.Events, event)
	}
	return op.Clone(), nil
}

func (d recordDocument) validatePosition(operationID string, position int64) error {
	if d.ID != recordID(operationID, d.Key) || d.OperationID != operationID || d.Position != position || position < 1 {
		return errors.New("operation record identity or position mismatch")
	}
	return nil
}

func (d recordDocument) task() (operations.Task, error) {
	var task operations.Task
	if err := decodeRecord(d.Data, &task); err != nil {
		return task, err
	}
	if !id.Valid(d.Key) || task.ID != d.Key {
		return operations.Task{}, errors.New("task identity does not match its record")
	}
	return task.Clone(), nil
}

func (d recordDocument) event() (operations.Event, error) {
	var event operations.Event
	if err := decodeRecord(d.Data, &event); err != nil {
		return event, err
	}
	if event.Sequence == 0 || eventKey(event.Sequence) != d.Key || event.OperationID != "" && event.OperationID != d.OperationID {
		return operations.Event{}, errors.New("event identity does not match its record")
	}
	return event, nil
}

func decodeRecord(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("operation record must contain exactly one JSON value")
	}
	return nil
}

func validateRecordID(recordID string) error {
	if !id.Valid(recordID) {
		return errors.New("operation record ID must be a canonical UUID")
	}
	return nil
}

func recordID(operationID, key string) string { return operationID + "/" + key }

// Fixed-width decimal strings preserve the full uint64 range and numeric order.
func eventKey(sequence uint64) string { return fmt.Sprintf("%020d", sequence) }

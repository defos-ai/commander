package executions

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/providers/mongo/internal/bsonvalue"
)

type executionDocument struct {
	ID          string                     `bson:"_id"`
	Command     messageDocument            `bson:"command"`
	Checkpoints map[string]json.RawMessage `bson:"checkpoints,omitempty"`
	Result      *messageDocument           `bson:"result,omitempty"`
	StartedAt   bsonvalue.Timestamp        `bson:"started_at"`
	CompletedAt *bsonvalue.Timestamp       `bson:"completed_at,omitempty"`
}

type messageDocument struct {
	ID            string               `bson:"id"`
	Type          messages.MessageType `bson:"type"`
	AgentID       string               `bson:"agent_id"`
	AdapterID     string               `bson:"adapter_id,omitempty"`
	CorrelationID string               `bson:"correlation_id,omitempty"`
	Work          messages.WorkContext `bson:"work"`
	Payload       json.RawMessage      `bson:"payload"`
	CreatedAt     bsonvalue.Timestamp  `bson:"created_at"`
	ExpiresAt     bsonvalue.Timestamp  `bson:"expires_at"`
}

func encodeMessage(message messages.Message) messageDocument {
	return messageDocument{
		ID: message.ID, Type: message.Type, AgentID: message.AgentID, AdapterID: message.AdapterID,
		CorrelationID: message.CorrelationID, Work: message.Work, Payload: bytes.Clone(message.Payload),
		CreatedAt: bsonvalue.NewTimestamp(message.CreatedAt), ExpiresAt: bsonvalue.NewTimestamp(message.ExpiresAt),
	}
}

func (d messageDocument) decode() (messages.Message, error) {
	createdAt, err := d.CreatedAt.Decode()
	if err != nil {
		return messages.Message{}, err
	}
	expiresAt, err := d.ExpiresAt.Decode()
	if err != nil {
		return messages.Message{}, err
	}
	return messages.Message{
		ID: d.ID, Type: d.Type, AgentID: d.AgentID, AdapterID: d.AdapterID,
		CorrelationID: d.CorrelationID, Work: d.Work, Payload: bytes.Clone(d.Payload), CreatedAt: createdAt, ExpiresAt: expiresAt,
	}, nil
}

func (d executionDocument) decode() (executions.Execution, error) {
	var record executions.Execution
	var err error
	if record.Command, err = d.Command.decode(); err != nil {
		return executions.Execution{}, err
	}
	if d.ID != record.Command.AgentID+"/"+record.Command.ID {
		return executions.Execution{}, errors.New("stored execution ID does not match its command")
	}
	if record.StartedAt, err = d.StartedAt.Decode(); err != nil {
		return executions.Execution{}, err
	}
	if d.Checkpoints != nil {
		record.Checkpoints = make(map[string]json.RawMessage, len(d.Checkpoints))
		for name, data := range d.Checkpoints {
			record.Checkpoints[name] = bytes.Clone(data)
		}
	}
	if d.Result != nil {
		result, err := d.Result.decode()
		if err != nil {
			return executions.Execution{}, err
		}
		record.Result = &result
	}
	if d.CompletedAt != nil {
		completedAt, err := d.CompletedAt.Decode()
		if err != nil {
			return executions.Execution{}, err
		}
		record.CompletedAt = &completedAt
	}
	if err := record.Validate(); err != nil {
		return executions.Execution{}, err
	}
	return record, nil
}

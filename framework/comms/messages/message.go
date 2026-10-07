package messages

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

type MessageType string

// AgentID is the reporting agent for events and the destination for commands.
type Message struct {
	ID            string          `json:"id"`
	Type          MessageType     `json:"type"`
	AgentID       string          `json:"agent_id,omitempty"`
	AdapterID     string          `json:"adapter_id,omitempty"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	Work          WorkContext     `json:"work,omitzero"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     time.Time       `json:"created_at"`
	// ExpiresAt limits when new command work may start; completed outcomes can still replay.
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

func NewMessage(kind MessageType, agentID string, payload any) (Message, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Message{}, fmt.Errorf("message payload: %w", err)
	}
	message := Message{
		ID: id.MessageID(), Type: kind, AgentID: agentID,
		Payload: encoded, CreatedAt: time.Now().UTC(),
	}
	if err := message.Validate(); err != nil {
		return Message{}, err
	}
	return message, nil
}

func (m Message) Validate() error {
	if err := m.Work.Validate(); err != nil {
		return err
	}
	if !id.Valid(m.ID) {
		return fmt.Errorf("message ID must be a UUID")
	}
	switch m.Type {
	case AdapterRequest, AdapterRequestCanceled, ControllerResponse:
		if !id.ValidAdapter(m.AdapterID) || m.AgentID != "" || m.Work != (WorkContext{}) {
			return fmt.Errorf("adapter messages require a valid adapter ID and no agent or work context")
		}
	default:
		if !id.ValidAgent(m.AgentID) || m.AdapterID != "" {
			return fmt.Errorf("agent messages require a valid agent ID and no adapter ID")
		}
	}
	if m.CorrelationID != "" && !id.Valid(m.CorrelationID) {
		return fmt.Errorf("message correlation ID must be a UUID")
	}
	if strings.TrimSpace(string(m.Type)) == "" {
		return fmt.Errorf("message type must not be blank")
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("message timestamp must be set")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(m.Payload, &payload); err != nil || payload == nil {
		return fmt.Errorf("message payload must be a JSON object")
	}
	return nil
}

// WorkContext connects a command and its reply to operation bookkeeping. The
// zero value is for commands issued outside an operation.
type WorkContext struct {
	OperationID string `json:"operation_id"`
	TaskID      string `json:"task_id"`
	StepID      string `json:"step_id,omitempty"`
}

func (w WorkContext) Validate() error {
	if w == (WorkContext{}) {
		return nil
	}
	if !id.Valid(w.OperationID) || !id.Valid(w.TaskID) {
		return fmt.Errorf("work requires operation and task UUIDs")
	}
	if w.StepID != "" && strings.TrimSpace(w.StepID) == "" {
		return fmt.Errorf("step ID must not be blank")
	}
	return nil
}

func NewAdapterMessage(kind MessageType, adapterID string, payload any) (Message, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Message{}, err
	}
	m := Message{ID: id.MessageID(), Type: kind, AdapterID: adapterID, Payload: data, CreatedAt: time.Now().UTC()}
	if err := m.Validate(); err != nil {
		return Message{}, err
	}
	return m, nil
}

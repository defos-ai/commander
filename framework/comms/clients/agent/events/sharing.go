package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

const (
	DataShared        = messages.DataShared
	MaxSharedDataSize = 256 * 1024
)

type SharedDataSource struct {
	ControllerID string `json:"controller_id,omitempty"`
	AgentID      string `json:"agent_id,omitempty"`
	CommandID    string `json:"command_id,omitempty"`
	ActionName   string `json:"action_name,omitempty"`
	OperationID  string `json:"operation_id"`
	TaskID       string `json:"task_id"`
}

type SharedData struct {
	Source    SharedDataSource     `json:"source"`
	BatchID   string               `json:"batch_id,omitempty"`
	Batch     *TaskBatchUpdate     `json:"batch,omitempty"`
	Result    json.RawMessage      `json:"result,omitempty"`
	Artifacts []artifacts.Artifact `json:"artifacts,omitempty"`
	Decision  *operations.Question `json:"decision,omitempty"`
}

type TaskBatchUpdate struct {
	Type     operations.EventType             `json:"type"`
	TaskIDs  []string                         `json:"task_ids"`
	Outcomes map[string]operations.TaskStatus `json:"outcomes,omitempty"`
}

func (source SharedDataSource) Validate() error {
	if !id.Valid(source.OperationID) || !id.Valid(source.TaskID) {
		return errors.New("shared data source requires operation and task UUIDs")
	}
	if source.ControllerID != "" {
		if !id.ValidRuntime(source.ControllerID, id.Controller) || source.AgentID != "" || source.CommandID != "" || source.ActionName != "" {
			return errors.New("controller shared data source requires a controller identity without agent execution fields")
		}
		return nil
	}
	if !id.ValidAgent(source.AgentID) || !id.Valid(source.CommandID) {
		return errors.New("shared data source requires agent and command identities")
	}
	if strings.TrimSpace(source.ActionName) == "" || !utf8.ValidString(source.ActionName) {
		return errors.New("shared data source requires a nonblank UTF-8 action name")
	}
	return nil
}

func (update SharedData) Validate() error {
	if err := update.Source.Validate(); err != nil {
		return err
	}
	if update.BatchID != "" && !id.Valid(update.BatchID) {
		return errors.New("shared data batch identity must be a UUID")
	}
	if update.Batch != nil {
		return update.validateTaskBatch()
	}
	if update.Source.ControllerID != "" {
		return errors.New("controller shared data update requires task batch lifecycle metadata")
	}
	if len(update.Result) == 0 && len(update.Artifacts) == 0 && update.Decision == nil {
		return errors.New("shared data requires a result, artifact or answered decision")
	}
	if len(update.Result) != 0 && (len(update.Result) > MaxSharedDataSize || !utf8.Valid(update.Result) || domain.ValidateObject(update.Result) != nil) {
		return errors.New("shared result must be a bounded UTF-8 JSON object")
	}
	seen := make(map[string]bool, len(update.Artifacts))
	for _, artifact := range update.Artifacts {
		if artifact.Validate() != nil || artifact.AgentID != update.Source.AgentID ||
			artifact.CommandID != update.Source.CommandID || artifact.ActionName != update.Source.ActionName {
			return errors.New("shared artifact must match its producing execution")
		}
		if seen[artifact.ID] {
			return errors.New("shared data contains duplicate artifact IDs")
		}
		seen[artifact.ID] = true
	}
	if update.Decision != nil {
		return validateSharedDecision(*update.Decision, update.Source)
	}
	return nil
}

func (update SharedData) validateTaskBatch() error {
	if update.Source.ControllerID == "" || update.BatchID == "" {
		return errors.New("task batch update requires controller origin and a batch UUID")
	}
	if len(update.Result) != 0 || len(update.Artifacts) != 0 || update.Decision != nil {
		return errors.New("task batch lifecycle update cannot contain producer data")
	}
	return update.Batch.validate(update.Source.TaskID)
}

func (batch TaskBatchUpdate) validate(parentID string) error {
	if len(batch.TaskIDs) == 0 {
		return errors.New("task batch update requires producer task IDs")
	}
	seen := make(map[string]bool, len(batch.TaskIDs))
	for _, taskID := range batch.TaskIDs {
		if !id.Valid(taskID) || taskID == parentID || seen[taskID] {
			return errors.New("task batch requires unique task UUIDs excluding its parent")
		}
		seen[taskID] = true
	}
	switch batch.Type {
	case operations.TaskBatchStarted:
		if len(batch.Outcomes) != 0 {
			return errors.New("task batch start cannot contain outcomes")
		}
	case operations.TaskBatchFinished:
		if len(batch.Outcomes) != len(batch.TaskIDs) {
			return errors.New("task batch finish requires one outcome per producer task")
		}
		for _, taskID := range batch.TaskIDs {
			switch batch.Outcomes[taskID] {
			case operations.TaskSucceeded, operations.TaskFailed, operations.TaskCancelled:
			default:
				return errors.New("task batch finish requires known producer outcomes")
			}
		}
	default:
		return errors.New("invalid task batch lifecycle event type")
	}
	return nil
}

func validateSharedDecision(question operations.Question, source SharedDataSource) error {
	if !id.Valid(question.ID) || question.TaskID != source.TaskID || question.Status != operations.QuestionAnswered ||
		question.AskedAt.IsZero() || !question.FallbackAt.After(question.AskedAt) || question.AnsweredAt == nil ||
		question.AnsweredAt.Before(question.AskedAt) || question.AnsweredBy.Validate() != nil {
		return errors.New("shared decision requires its saved task-scoped answer and origin")
	}
	if strings.TrimSpace(question.Text) == "" || !utf8.ValidString(question.Text) ||
		strings.TrimSpace(question.Answer) == "" || !utf8.ValidString(question.Answer) || len(question.Options) > 4 {
		return errors.New("shared decision text and answer must contain nonblank UTF-8 text")
	}
	for _, option := range question.Options {
		if strings.TrimSpace(option) == "" || !utf8.ValidString(option) {
			return errors.New("shared decision options must contain nonblank UTF-8 text")
		}
	}
	if question.CommandID != "" || question.Key != "" {
		if question.CommandID != source.CommandID || operations.ValidateExecutionQuestion(question.Key, question.Text, question.Options) != nil ||
			len(question.Answer) > operations.MaxExecutionQuestionAnswerBytes {
			return errors.New("shared execution decision must match its source command")
		}
	}
	if question.AnsweredBy == operations.AnswerModel && question.AnsweredAt.Before(question.FallbackAt) {
		return errors.New("shared model decision precedes its operator-first deadline")
	}
	return nil
}

func (update SharedData) Clone() SharedData {
	update.Result = bytes.Clone(update.Result)
	update.Artifacts = artifacts.CloneAll(update.Artifacts)
	if update.Batch != nil {
		batch := *update.Batch
		batch.TaskIDs = slices.Clone(batch.TaskIDs)
		batch.Outcomes = maps.Clone(batch.Outcomes)
		update.Batch = &batch
	}
	if update.Decision != nil {
		question := update.Decision.Clone()
		update.Decision = &question
	}
	return update
}

// NewSharedData targets one active execution. The controller must separately
// verify saved evidence and the operation's sharing policy before publishing.
func NewSharedData(agentID, commandID, operationID, taskID string, update SharedData) (messages.Message, error) {
	message, err := messages.NewMessage(DataShared, agentID, update)
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID = commandID
	message.OperationID, message.TaskID = operationID, taskID
	if _, err := DecodeSharedData(message); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

func DecodeSharedData(message messages.Message) (SharedData, error) {
	if len(message.Payload) > MaxSharedDataSize || !utf8.Valid(message.Payload) {
		return SharedData{}, errors.New("shared data exceeds its size limit or contains invalid UTF-8")
	}
	if err := message.Validate(); err != nil {
		return SharedData{}, err
	}
	if message.Type != DataShared || !id.Valid(message.CorrelationID) || message.OperationID == "" || !message.ExpiresAt.IsZero() {
		return SharedData{}, errors.New("shared data requires operation work, target command correlation and no expiry")
	}
	var update SharedData
	if err := domain.Decode(message.Payload, &update); err != nil {
		return SharedData{}, errors.New("invalid shared data")
	}
	if update.Source.OperationID != message.OperationID || update.Source.TaskID == message.TaskID {
		return SharedData{}, errors.New("shared data must come from another task in the same operation")
	}
	if update.Batch != nil && slices.Contains(update.Batch.TaskIDs, message.TaskID) {
		return SharedData{}, errors.New("shared batch receiver must be outside the producer tasks")
	}
	return update, nil
}

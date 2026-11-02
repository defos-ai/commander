package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

const (
	ContextShared        = messages.ContextShared
	MaxContextUpdateSize = 256 * 1024
)

type ContextSource struct {
	AgentID    string               `json:"agent_id"`
	CommandID  string               `json:"command_id"`
	ActionName string               `json:"action_name"`
	Work       messages.WorkContext `json:"work"`
}

type ContextUpdate struct {
	Source    ContextSource        `json:"source"`
	Result    json.RawMessage      `json:"result,omitempty"`
	Artifacts []artifacts.Artifact `json:"artifacts,omitempty"`
	Decision  *operations.Question `json:"decision,omitempty"`
}

func (source ContextSource) Validate() error {
	if !id.ValidAgent(source.AgentID) || !id.Valid(source.CommandID) {
		return errors.New("shared context source requires agent and command identities")
	}
	if strings.TrimSpace(source.ActionName) == "" || !utf8.ValidString(source.ActionName) {
		return errors.New("shared context source requires a nonblank UTF-8 action name")
	}
	if source.Work.OperationID == "" || source.Work.Validate() != nil {
		return errors.New("shared context source requires operation and task UUIDs")
	}
	return nil
}

func (update ContextUpdate) Validate() error {
	if err := update.Source.Validate(); err != nil {
		return err
	}
	if len(update.Result) == 0 && len(update.Artifacts) == 0 && update.Decision == nil {
		return errors.New("shared context requires a result, artifact or answered decision")
	}
	if len(update.Result) != 0 && (len(update.Result) > MaxContextUpdateSize || !utf8.Valid(update.Result) || domain.ValidateObject(update.Result) != nil) {
		return errors.New("shared result must be a bounded UTF-8 JSON object")
	}
	seen := make(map[string]bool, len(update.Artifacts))
	for _, artifact := range update.Artifacts {
		if artifact.Validate() != nil || artifact.AgentID != update.Source.AgentID ||
			artifact.CommandID != update.Source.CommandID || artifact.ActionName != update.Source.ActionName {
			return errors.New("shared artifact must match its producing execution")
		}
		if seen[artifact.ID] {
			return errors.New("shared context contains duplicate artifact IDs")
		}
		seen[artifact.ID] = true
	}
	if update.Decision != nil {
		return validateContextDecision(*update.Decision, update.Source)
	}
	return nil
}

func validateContextDecision(question operations.Question, source ContextSource) error {
	if !id.Valid(question.ID) || question.TaskID != source.Work.TaskID || question.Status != operations.QuestionAnswered ||
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

func (update ContextUpdate) Clone() ContextUpdate {
	update.Result = bytes.Clone(update.Result)
	update.Artifacts = artifacts.CloneAll(update.Artifacts)
	if update.Decision != nil {
		question := update.Decision.Clone()
		update.Decision = &question
	}
	return update
}

// NewContextUpdate targets one active execution. The controller must separately
// verify saved evidence and the operation's sharing policy before publishing.
func NewContextUpdate(agentID, commandID string, work messages.WorkContext, update ContextUpdate) (messages.Message, error) {
	message, err := messages.NewMessage(ContextShared, agentID, update)
	if err != nil {
		return messages.Message{}, err
	}
	message.CorrelationID, message.Work = commandID, work
	if _, err := DecodeContextUpdate(message); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

func DecodeContextUpdate(message messages.Message) (ContextUpdate, error) {
	if len(message.Payload) > MaxContextUpdateSize || !utf8.Valid(message.Payload) {
		return ContextUpdate{}, errors.New("shared context exceeds its size limit or contains invalid UTF-8")
	}
	if err := message.Validate(); err != nil {
		return ContextUpdate{}, err
	}
	if message.Type != ContextShared || !id.Valid(message.CorrelationID) || message.Work.OperationID == "" || !message.ExpiresAt.IsZero() {
		return ContextUpdate{}, errors.New("shared context requires operation work, target command correlation and no expiry")
	}
	var update ContextUpdate
	if err := domain.Decode(message.Payload, &update); err != nil {
		return ContextUpdate{}, errors.New("invalid shared context")
	}
	if update.Source.Work.OperationID != message.Work.OperationID || update.Source.Work.TaskID == message.Work.TaskID {
		return ContextUpdate{}, errors.New("shared context must come from another task in the same operation")
	}
	return update, nil
}

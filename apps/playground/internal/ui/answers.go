package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/apps/playground/internal/chat"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/id"
)

type operatorAnswerResult struct {
	request   chat.Event
	operation bridge.Operation
	err       error
}

func (m *Model) submitOperatorAnswer(request chat.Event) tea.Cmd {
	key := questionKey{request.OperationID, request.QuestionID}
	operation := m.operations[key.operationID]
	pending := false
	for _, question := range m.pendingQuestions(operation, time.Now()) {
		pending = pending || question.key == key
	}
	if !id.Valid(key.operationID) || !id.Valid(key.questionID) || strings.TrimSpace(request.Answer) == "" || !pending {
		m.answerFailed(request, errors.New("This question is no longer pending or has not been loaded."))
		return nil
	}
	if m.questions.cancel != nil {
		m.answerFailed(request, errors.New("An answer is being submitted; wait for its outcome."))
		return nil
	}
	api, err := client.New(client.Connection{URL: m.config.API.URL, Credentials: m.config.API.Credentials})
	if err != nil {
		return m.connectForAnswer(request, err.Error())
	}
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	done := make(chan struct{})
	results := make(chan operatorAnswerResult, 1)
	m.questions.cancel, m.questions.done = cancel, done
	m.receive(chat.Event{Kind: "answer_start", OperationID: key.operationID, QuestionID: key.questionID})
	m.controls++
	m.refresh()
	go func() {
		defer close(done)
		defer cancel()
		defer api.Close()
		operation, err := postOperatorAnswer(ctx, api, request)
		results <- operatorAnswerResult{request: request, operation: operation, err: err}
	}()
	return func() tea.Msg { return <-results }
}

func (m *Model) connectForAnswer(request chat.Event, message string) tea.Cmd {
	if current := m.questions.current; current != nil && current.key == (questionKey{request.OperationID, request.QuestionID}) {
		m.questions.submitting = false
	}
	if m.secrets.open {
		m.answerFailed(request, errors.New("Finish the open form, then submit your answer again."))
		return nil
	}
	message += ". Connect to the API to submit this answer, or answer in Admin UI."
	return m.connectAdminAPI(message, func() tea.Cmd { return m.submitOperatorAnswer(request) })
}

func (m *Model) operatorAnswerResult(result operatorAnswerResult) tea.Cmd {
	m.questions.cancel()
	<-m.questions.done
	m.questions.cancel, m.questions.done = nil, nil
	m.controls = max(0, m.controls-1)
	if result.err != nil {
		if errors.Is(result.err, client.ErrAuthentication) && !errors.Is(result.err, client.ErrWriteUncertain) {
			m.config.API.Credentials.Password = ""
			return m.connectForAnswer(result.request, result.err.Error())
		}
		m.answerFailed(result.request, result.err)
		return nil
	}
	m.receive(chat.Event{Kind: "answer_end", OperationID: result.request.OperationID, QuestionID: result.request.QuestionID, Answer: result.request.Answer})
	if err := m.engine.ObserveOperation(result.operation); err != nil {
		m.add("notice", "Answer saved. "+err.Error())
	}
	m.refresh()
	return nil
}

func (m *Model) answerFailed(request chat.Event, err error) {
	m.receive(chat.Event{Kind: "answer_end", OperationID: request.OperationID, QuestionID: request.QuestionID, Text: err.Error()})
}

func postOperatorAnswer(ctx context.Context, api *client.Client, request chat.Event) (bridge.Operation, error) {
	body, err := json.Marshal(struct {
		Answer string `json:"answer"`
	}{request.Answer})
	if err != nil {
		return bridge.Operation{}, errors.New("Could not encode the answer.")
	}
	defer clear(body)
	path := "/api/v1/operations/" + request.OperationID + "/questions/" + request.QuestionID + "/answer"
	response, err := api.Do(ctx, http.MethodPost, path, body, 4<<20)
	if err != nil {
		return bridge.Operation{}, err
	}
	var operation bridge.Operation
	if response.StatusCode != http.StatusOK || json.Unmarshal(response.Data, &operation) != nil || operation.ID != request.OperationID {
		return bridge.Operation{}, client.ErrWriteUncertain
	}
	for _, question := range operation.Questions {
		if question.ID == request.QuestionID && question.Status == bridge.QuestionAnswered && question.Answer == request.Answer {
			operation.Raw = response.Data
			return operation, nil
		}
	}
	return bridge.Operation{}, client.ErrWriteUncertain
}

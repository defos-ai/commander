package fakes

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type QuestionAPIOptions struct {
	Password     string
	Status       int
	ResponseBody string
}

type QuestionAnswerRequest struct {
	Method, Path, Username, Password, Answer string
}

type QuestionServer struct {
	server     *mcp.Server
	mu         sync.Mutex
	operation  bridge.Operation
	questionID string
	answers    chan string
	requests   chan QuestionAnswerRequest
	mcpAnswers int
	api        QuestionAPIOptions
}

func NewQuestionServer(t *testing.T, operation bridge.Operation, questionID string, api QuestionAPIOptions) *QuestionServer {
	t.Helper()
	encoded, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot bridge.Operation
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	questions := &QuestionServer{operation: snapshot, questionID: questionID, answers: make(chan string, 1),
		requests: make(chan QuestionAnswerRequest, 16), api: api,
		server: mcp.NewServer(&mcp.Implementation{Name: "question-fixture", Version: "1"}, nil)}
	for _, name := range []string{"get_operation", "operation_events", "answer_question"} {
		questions.server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, questions.call)
	}
	return questions
}

func (s *QuestionServer) Answers() <-chan string { return s.answers }

func (s *QuestionServer) APIRequests() []QuestionAnswerRequest {
	var requests []QuestionAnswerRequest
	for {
		select {
		case request := <-s.requests:
			requests = append(requests, request)
		default:
			return requests
		}
	}
}

func (s *QuestionServer) MCPAnswerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mcpAnswers
}

func (s *QuestionServer) ClearPendingCount() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operation.Progress.PendingQuestions = 0
}

func (s *QuestionServer) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.server }, nil)
}

func (s *QuestionServer) APIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/questions/{question_id}/answer", s.answer)
	return mux
}

func (s *QuestionServer) answer(w http.ResponseWriter, r *http.Request) {
	request := QuestionAnswerRequest{Method: r.Method, Path: r.URL.Path}
	request.Username, request.Password, _ = r.BasicAuth()
	defer func() { s.requests <- request }()
	w.Header().Set("Content-Type", "application/json")
	if request.Username != "commander" || request.Password != s.api.Password {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}
	var input struct {
		Answer string `json:"answer"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	request.Answer = input.Answer
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.PathValue("operation_id") != s.operation.ID || r.PathValue("question_id") != s.questionID {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if s.api.Status != 0 || s.api.ResponseBody != "" {
		status := s.api.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(s.api.ResponseBody))
		return
	}
	now := time.Now()
	s.operation.Progress.PendingQuestions = 0
	for i := range s.operation.Questions {
		question := &s.operation.Questions[i]
		if question.ID == s.questionID {
			if !question.Pending(now) {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"question_expired"}`))
				return
			}
			question.Status, question.Answer = bridge.QuestionAnswered, input.Answer
			answeredAt := now.Format(time.RFC3339Nano)
			question.AnsweredAt = &answeredAt
		} else if question.Pending(now) {
			question.ExpiresAt = now.Add(500 * time.Millisecond)
			s.operation.Progress.PendingQuestions++
		}
	}
	s.operation.UpdatedAt = now
	s.answers <- input.Answer
	_ = json.NewEncoder(w).Encode(s.operation)
}

func (s *QuestionServer) call(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var payload any = s.operation
	switch req.Params.Name {
	case "operation_events":
		payload = map[string]any{"events": []any{}, "next_cursor": ""}
	case "answer_question":
		s.mcpAnswers++
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Operator answers must use the API"}}}, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
}

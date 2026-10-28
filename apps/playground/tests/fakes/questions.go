package fakes

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type QuestionServer struct {
	t          *testing.T
	server     *mcp.Server
	operation  bridge.Operation
	questionID string
	answers    chan string
}

func NewQuestionServer(t *testing.T, operation bridge.Operation, questionID string) *QuestionServer {
	t.Helper()
	encoded, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot bridge.Operation
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	questions := &QuestionServer{t: t, operation: snapshot, questionID: questionID, answers: make(chan string, 1),
		server: mcp.NewServer(&mcp.Implementation{Name: "question-fixture", Version: "1"}, nil)}
	for _, name := range []string{"get_operation", "operation_events", "answer_question"} {
		questions.server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, questions.call)
	}
	return questions
}

func (s *QuestionServer) Answers() <-chan string { return s.answers }

func (s *QuestionServer) ClearPendingCount() {
	s.operation.Progress.PendingQuestions = 0
}

func (s *QuestionServer) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.server }, nil)
}

func (s *QuestionServer) call(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var payload any = s.operation
	switch req.Params.Name {
	case "operation_events":
		payload = map[string]any{"events": []any{}, "next_cursor": ""}
	case "answer_question":
		var args struct {
			Answer      string
			OperationID string `json:"operation_id"`
			QuestionID  string `json:"question_id"`
		}
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.OperationID != s.operation.ID || args.QuestionID != s.questionID {
			s.t.Errorf("answer targeted the wrong question: %+v", args)
		}
		select {
		case s.answers <- args.Answer:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.operation.UpdatedAt = time.Now()
		s.operation.Questions[1].Status = bridge.QuestionAnswered
		s.operation.Questions[1].Answer = args.Answer
		s.operation.Progress.PendingQuestions = 1
		s.operation.Questions[0].ExpiresAt = time.Now().Add(500 * time.Millisecond)
		payload = s.operation
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
}

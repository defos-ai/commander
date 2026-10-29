package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	operationendpoints "github.com/defos-ai/commander/apps/adapters/api/endpoints/operations"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestHTTPQuestionAnswerOriginIsAssignedByAdapter(t *testing.T) {
	const operationID = "00000000-0000-4000-8000-000000000001"
	const questionID = "00000000-0000-4000-8000-000000000002"
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"answer", `{"answer":"Proceed"}`, http.StatusOK},
		{"origin override", `{"answer":"Proceed","answered_by":"model"}`, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			commands := fakes.NewQuestionAnswers()
			mux := http.NewServeMux()
			operationendpoints.Register(mux, nil, nil, nil, nil, commands)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/operations/"+operationID+"/questions/"+questionID+"/answer", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			mux.ServeHTTP(recorder, request)

			if recorder.Code != test.status {
				t.Fatalf("answer status=%d, want %d", recorder.Code, test.status)
			}
			if test.status != http.StatusOK {
				select {
				case <-commands.Submitted():
					t.Fatal("client-supplied answer origin reached the controller")
				default:
				}
				return
			}
			select {
			case command := <-commands.Submitted():
				if command.OperationID != operationID || command.QuestionID != questionID || command.Answer != "Proceed" || command.AnsweredBy != operations.AnswerOperator {
					t.Fatal("HTTP answer lost its target, value or adapter-assigned origin")
				}
			default:
				t.Fatal("HTTP answer did not reach the controller")
			}
			var saved models.OperationResponse
			if json.Unmarshal(recorder.Body.Bytes(), &saved) != nil || len(saved.Questions) != 1 || saved.Questions[0].AnsweredBy != "operator" {
				t.Fatal("HTTP response omitted the saved answer origin")
			}
		})
	}
}

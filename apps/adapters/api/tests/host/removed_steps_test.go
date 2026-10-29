package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
)

func TestAPIRemovedMissionStepsAreRejected(t *testing.T) {
	handler := hostfixtures.LogHandler(t, hostfixtures.LogAPIConfig(t))
	const operation = "/api/v1/operations/10000000-0000-4000-8000-000000000001"
	for _, test := range []struct{ name, path, body string }{
		{"mission", "/api/v1/missions", `{"name":"review","objectives":["Review"],"input_schema":{},"output_schema":{},"steps":[]}`},
		{"task", operation + "/tasks", `{"action_name":"inspect","input":{},"step_id":"review"}`},
		{"question", operation + "/questions", `{"text":"Continue?","step_id":"review"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost"+test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.SetBasicAuth("log-user", "log-password")
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			assertAPIError(t, hostfixtures.Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}, http.StatusBadRequest, "invalid_request")
		})
	}
}

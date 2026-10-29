package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestHTTPCompletionOriginIsAssignedByAdapter(t *testing.T) {
	for _, scenario := range []struct {
		name, body string
		status     int
	}{
		{"output", `{"output":{"summary":"Reviewed"}}`, http.StatusOK},
		{"operator override", `{"output":{},"completed_by":"operator"}`, http.StatusBadRequest},
		{"model override", `{"output":{},"completed_by":"model"}`, http.StatusBadRequest},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			config := hostfixtures.LogAPIConfig(t)
			commands := fakes.NewOperationCompletions(nil)
			config.Operations = commands
			handler := hostfixtures.LogHandler(t, config)
			operationID := id.OperationID()

			response := hostfixtures.MissionRequest(t.Context(), handler, http.MethodPost,
				"/api/v1/operations/"+operationID+"/complete", scenario.body, true)

			command, calls := commands.Snapshot()
			if scenario.status != http.StatusOK {
				assertAPIError(t, response, scenario.status, "invalid_request")
				if calls.Attempts != 0 {
					t.Fatal("a client-supplied completion origin reached the controller")
				}
				return
			}
			saved := decodeResponse[models.OperationResponse](t, response)
			if calls.Attempts != 1 || command.OperationID != operationID || command.CompletedBy != operations.CompletionOperator ||
				string(command.Output) != `{"summary":"Reviewed"}` {
				t.Fatal("HTTP completion lost its target, output or adapter-assigned origin")
			}
			if saved.Status != "completed" || saved.CompletedBy != "operator" || saved.ReportID == "" {
				t.Fatal("HTTP completion response omitted the saved outcome or origin")
			}
		})
	}
}

func TestCompletionPolicyErrorMappingPreservesUncertainOutcomes(t *testing.T) {
	for _, scenario := range []struct {
		code   string
		err    error
		status int
	}{
		{"completion_operator_required", operations.ErrCompletionOperatorRequired, http.StatusConflict},
		{"request_timeout", errors.Join(context.DeadlineExceeded, operations.ErrCompletionOperatorRequired), http.StatusGatewayTimeout},
		{"operation_write_uncertain", errors.Join(operations.ErrWriteUncertain, operations.ErrCompletionOperatorRequired, context.DeadlineExceeded), http.StatusServiceUnavailable},
		{"command_outcome_unknown", errors.Join(executions.ErrIndeterminate, operations.ErrCompletionOperatorRequired), http.StatusServiceUnavailable},
	} {
		t.Run(scenario.code, func(t *testing.T) {
			config := hostfixtures.LogAPIConfig(t)
			commands := fakes.NewOperationCompletions(scenario.err)
			config.Operations = commands
			handler := hostfixtures.LogHandler(t, config)

			response := hostfixtures.MissionRequest(t.Context(), handler, http.MethodPost,
				"/api/v1/operations/"+id.OperationID()+"/complete", `{"output":{}}`, true)

			assertAPIError(t, response, scenario.status, scenario.code)
			_, calls := commands.Snapshot()
			if calls.Attempts != 1 || calls.Remaining <= 0 || calls.Remaining > 10*time.Second {
				t.Fatal("completion was retried or lost its bounded command context")
			}
		})
	}
}

func TestCompletionOpenAPIKeepsOriginReadOnly(t *testing.T) {
	handler := hostfixtures.LogHandler(t, hostfixtures.LogAPIConfig(t))
	document := decodeResponse[struct {
		Paths map[string]map[string]struct {
			Description string `json:"description"`
			Responses   map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}](t, hostfixtures.LogRequest(t.Context(), handler, "/openapi.json", true))
	var operation, request jsonschema.Schema
	if err := json.Unmarshal(document.Components.Schemas["Operation"], &operation); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(document.Components.Schemas["CompleteOperationRequest"], &request); err != nil {
		t.Fatal(err)
	}

	resolved, err := request.Resolve(nil)

	if err != nil {
		t.Fatal(err)
	}
	origin := operation.Properties["completed_by"]
	if origin == nil || !origin.ReadOnly || !reflect.DeepEqual(origin.Enum, []any{"operator", "model"}) || slices.Contains(operation.Required, "completed_by") {
		t.Fatal("completion origin must be an optional read-only operator/model response field")
	}
	if request.Properties["completed_by"] != nil || !reflect.DeepEqual(request.Required, []string{"output"}) {
		t.Fatal("completion input must remain output-only")
	}
	if err := resolved.Validate(map[string]any{"output": nil}); err != nil {
		t.Fatal("completion schema rejected an explicit output")
	}
	if err := resolved.Validate(map[string]any{"output": nil, "completed_by": "operator"}); err == nil {
		t.Fatal("completion schema accepted a caller-supplied origin")
	}
	endpoint := document.Paths["/api/v1/operations/{operation_id}/complete"]["post"]
	if !strings.Contains(endpoint.Responses["409"].Description, "completion_operator_required") || !strings.Contains(endpoint.Description, "operator origin") {
		t.Fatal("completion OpenAPI omits the operator policy or rejection")
	}
}

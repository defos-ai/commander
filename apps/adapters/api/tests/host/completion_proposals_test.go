package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	fixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestHTTPCompletionProposalAssignsOriginAndPreservesJSON(t *testing.T) {
	for _, scenario := range []struct {
		name, body string
		status     int
	}{
		{"output", `{"output":{"identifier":9007199254740993,"path":"\u0061\\path"}}`, http.StatusOK},
		{"null output", `{"output":null}`, http.StatusOK},
		{"missing output", `{}`, http.StatusBadRequest},
		{"chosen origin", `{"output":{},"proposed_by":"model"}`, http.StatusBadRequest},
		{"chosen proposal", `{"output":{},"id":"00000000-0000-4000-8000-000000000001"}`, http.StatusBadRequest},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			config := fixtures.LogAPIConfig(t)
			commands := fakes.NewCompletionProposals(nil)
			config.Operations = commands
			handler := fixtures.LogHandler(t, config)
			operationID := id.OperationID()

			response := fixtures.MissionRequest(t.Context(), handler, http.MethodPost,
				"/api/v1/operations/"+operationID+"/completion-proposals", scenario.body, true)

			command, calls := commands.Snapshot()
			if scenario.status != http.StatusOK {
				assertAPIError(t, response, scenario.status, "invalid_request")
				if calls.Attempts != 0 {
					t.Fatal("invalid proposal reached the controller")
				}
				return
			}
			saved := decodeResponse[models.OperationResponse](t, response)
			if calls.Attempts != 1 || command.OperationID != operationID || command.ProposedBy != operations.CompletionOperator ||
				saved.Status != "planning" || saved.ReportID != "" || saved.CompletedBy != "" || len(saved.Output) != 0 ||
				saved.CompletionProposal == nil || saved.CompletionProposal.ProposedBy != "operator" || saved.CompletionProposal.ID == "" {
				t.Fatal("proposal lost its adapter-assigned provenance or looked like accepted completion")
			}
			if scenario.name == "output" && (!strings.Contains(response.Body, "9007199254740993") || !strings.Contains(response.Body, `\u0061\\path`)) {
				t.Fatal("proposal response changed JSON number or string tokens")
			}
			if !reflect.DeepEqual(saved.CompletionProposal.Output, command.Output) {
				t.Fatal("proposal response changed the submitted output")
			}
		})
	}
}

func TestCompletionProposalReadbackAndMappingAreIndependent(t *testing.T) {
	config := fixtures.LogAPIConfig(t)
	operation := fixtures.LogOperation()
	operation.CompletionProposal = &operations.CompletionProposal{
		ID: id.New(), Output: json.RawMessage(`{"identifier":9007199254740993}`), ProposedBy: operations.CompletionModel, ProposedAt: operation.CreatedAt,
	}
	if err := config.OperationStore.Create(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	handler := fixtures.LogHandler(t, config)

	response := fixtures.LogRequest(t.Context(), handler, "/api/v1/operations/"+operation.ID, true)
	saved := decodeResponse[models.OperationResponse](t, response)
	mapped := mappings.ToOperationResponse(operation)

	if saved.CompletionProposal == nil || saved.CompletionProposal.ID != operation.CompletionProposal.ID ||
		saved.CompletionProposal.ProposedBy != "model" || !saved.CompletionProposal.ProposedAt.Equal(operation.CreatedAt) ||
		!strings.Contains(response.Body, "9007199254740993") {
		t.Fatal("saved operation query omitted proposal content or provenance")
	}
	mapped.CompletionProposal.Output[0] = '['
	if operation.CompletionProposal.Output[0] != '{' {
		t.Fatal("API projection shares mutable proposal output")
	}
}

func TestHTTPCompletionProposalPreservesFailureClassification(t *testing.T) {
	for _, scenario := range []struct {
		code    string
		failure error
		status  int
	}{
		{"operation_active_tasks", operations.ErrActiveTasks, http.StatusConflict},
		{"invalid_output", domain.ErrInvalidOutput, http.StatusUnprocessableEntity},
		{"operation_write_uncertain", errors.Join(operations.ErrWriteUncertain, operations.ErrActiveTasks), http.StatusServiceUnavailable},
	} {
		t.Run(scenario.code, func(t *testing.T) {
			config := fixtures.LogAPIConfig(t)
			commands := fakes.NewCompletionProposals(scenario.failure)
			config.Operations = commands
			handler := fixtures.LogHandler(t, config)

			response := fixtures.MissionRequest(t.Context(), handler, http.MethodPost,
				"/api/v1/operations/"+id.OperationID()+"/completion-proposals", `{"output":{}}`, true)

			assertAPIError(t, response, scenario.status, scenario.code)
			_, calls := commands.Snapshot()
			if calls.Attempts != 1 || calls.Remaining <= 0 || calls.Remaining > 10*time.Second {
				t.Fatal("proposal lost its bounded context or was retried")
			}
		})
	}
}

func TestCompletionProposalOpenAPIKeepsProvenanceReadOnly(t *testing.T) {
	handler := fixtures.LogHandler(t, fixtures.LogAPIConfig(t))
	document := decodeResponse[struct {
		Paths map[string]map[string]struct {
			Description string `json:"description"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}](t, fixtures.LogRequest(t.Context(), handler, "/openapi.json", true))
	var operation, proposal, request jsonschema.Schema
	for name, target := range map[string]*jsonschema.Schema{"Operation": &operation, "CompletionProposal": &proposal, "ProposeCompletionRequest": &request} {
		if err := json.Unmarshal(document.Components.Schemas[name], target); err != nil {
			t.Fatal(err)
		}
	}

	resolved, err := request.Resolve(nil)

	if err != nil {
		t.Fatal(err)
	}
	if operation.Properties["completion_proposal"] == nil || !operation.Properties["completion_proposal"].ReadOnly || slices.Contains(operation.Required, "completion_proposal") ||
		!proposal.ReadOnly || !reflect.DeepEqual(proposal.Properties["proposed_by"].Enum, []any{"operator", "model"}) {
		t.Fatal("proposal response must expose optional read-only provenance")
	}
	if !reflect.DeepEqual(request.Required, []string{"output"}) || request.Properties["proposed_by"] != nil {
		t.Fatal("proposal input must remain output-only")
	}
	if err := resolved.Validate(map[string]any{"output": nil}); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(map[string]any{"output": nil, "proposed_by": "model"}); err == nil {
		t.Fatal("proposal schema accepted chosen provenance")
	}
	endpoint := document.Paths["/api/v1/operations/{operation_id}/completion-proposals"]["post"]
	if !strings.Contains(endpoint.Description, "does not complete") {
		t.Fatal("proposal endpoint omits nonterminal semantics")
	}
}

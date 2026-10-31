package e2e_test

import (
	"encoding/json"
	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/assessments"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	"strings"
	"testing"
	"time"
)

func TestE2EAssessmentScopeEvidenceAndAutomaticWork(t *testing.T) {
	s := e2efixtures.NewSessionStack(t, "")
	s.Worker(1)
	destination := map[string]any{"type": "object", "properties": map[string]any{"destination": map[string]any{"type": "string", "x-defos-destination": true}}, "required": []string{"destination"}}
	s.Request("POST", "/api/v1/missions", map[string]any{"name": "scoped-assessment", "objectives": []string{"Retain evidence within the accepted environment"},
		"required_actions": []string{"assessment-probe"}, "input_schema": destination, "output_schema": map[string]any{"type": "object"}}, 201, nil)
	scope := &assessments.Snapshot{TargetID: id.New(), TargetName: "Office", Revision: 7, NetworkID: "office", Scope: assessments.Scope{Networks: []assessments.Network{{ID: "office", Name: "Office", CIDRs: []string{"10.0.0.0/24"}}}, Domains: []assessments.Domain{}}}
	s.Request("POST", "/api/v1/operations", map[string]any{"mission_name": "scoped-assessment", "input": map[string]string{"destination": "203.0.113.5"}, "assessment": scope}, 422, nil)
	var started operations.Operation
	s.Request("POST", "/api/v1/operations", map[string]any{"mission_name": "scoped-assessment", "input": map[string]string{"destination": "10.0.0.5"}, "assessment": scope}, 201, &started)
	s.Restart()
	task := s.Assign(started.ID, "assessment-probe", map[string]string{"destination": "10.0.0.5", "next_destination": "203.0.113.5"})
	outcome := s.Finished(started.ID, task.ID)
	var output struct {
		Destination string `json:"destination"`
		Revision    int    `json:"scope_revision"`
	}
	if err := json.Unmarshal(outcome.Result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.Destination != "10.0.0.5" || output.Revision != 7 || outcome.Result.Observations == nil || len(outcome.Result.Observations.Records) != 1 {
		t.Fatalf("action response lost scope or evidence: %+v %+v", output, outcome.Result)
	}
	e2efixtures.WaitUntil(t, 10*time.Second, func() bool { return s.Operation(started.ID).Status == operations.Failed })
	failed := s.Operation(started.ID)
	if !strings.Contains(failed.Error, "outside operation assessment scope") || len(failed.Tasks) != 2 || failed.Tasks[1].Result != nil || failed.Tasks[1].AgentID != "" {
		t.Fatalf("automatic out-of-scope task was not rejected before dispatch: %+v", failed)
	}
	if failed.Assessment == nil || failed.Assessment.Revision != 7 || failed.Tasks[0].Result.Observations.Records[0].Address != "10.0.0.5" {
		t.Fatal("failed operation lost its saved scope or collected evidence")
	}
	mcp := s.MCP()
	var mirrored operations.Operation
	mcp.Call("get_operation", map[string]string{"operation_id": started.ID}, &mirrored)
	if mirrored.Assessment == nil || mirrored.Assessment.Revision != 7 || mirrored.Tasks[0].Result.Observations == nil {
		t.Fatal("MCP dropped scope/evidence")
	}
	// Exact domains do not authorize siblings or their resolved addresses.
	domainScope := &assessments.Snapshot{TargetID: scope.TargetID, TargetName: "Web", Revision: 8, Scope: assessments.Scope{Networks: []assessments.Network{}, Domains: []assessments.Domain{{Hostname: "example.test"}}}}
	for _, address := range []string{"api.example.test", "203.0.113.5"} {
		s.Request("POST", "/api/v1/operations", map[string]any{"mission_name": "scoped-assessment", "input": map[string]string{"destination": address}, "assessment": domainScope}, 422, nil)
	}
	domainScope.Scope.Domains[0].IncludeSubdomains = true
	var domainRun operations.Operation
	mcp.Call("start_operation", map[string]any{"mission_name": "scoped-assessment", "input": map[string]string{"destination": "api.example.test"}, "assessment": domainScope}, &domainRun)
	if domainRun.Assessment == nil || domainRun.Assessment.Revision != 8 {
		t.Fatal("MCP start did not preserve assessment")
	}
	// Overlapping address spaces require the caller to select the intended Network.
	ambiguous := scope.Clone()
	ambiguous.NetworkID = ""
	ambiguous.Scope.Networks = append(ambiguous.Scope.Networks, assessments.Network{ID: "lab", Name: "Lab", CIDRs: []string{"10.0.0.0/24"}})
	s.Request("POST", "/api/v1/operations", map[string]any{"mission_name": "scoped-assessment", "input": map[string]string{"destination": "10.0.0.5"}, "assessment": ambiguous}, 422, nil)
}

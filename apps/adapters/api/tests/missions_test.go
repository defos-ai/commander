package api_test

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
)

func TestMissionCatalogAndRepeatedCleanup(t *testing.T) {
	a := fixtures.Connect(t)
	catalog := fixtures.Read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions")
	if !fixtures.HasExpectedMissions(catalog.Items) {
		t.Fatalf("unexpected stack missions: %+v", catalog)
	}
	build := fixtures.Read[models.MissionResponse](a, "/api/v1/missions/build-agent")
	buildIndex := slices.IndexFunc(catalog.Items, func(item models.MissionResponse) bool { return item.Name == build.Name })
	if buildIndex < 0 || !reflect.DeepEqual(catalog.Items[buildIndex], build) || !slices.Equal(build.RequiredActions, []string{"checkout-template", "build-agent"}) || !slices.Equal(build.OptionalActions, []string{"publish-agent", "release-agent"}) {
		t.Fatalf("build mission lost its optional publication action: %+v", build)
	}
	assertSchema(t, a, "Mission", a.Request(http.MethodGet, "/api/v1/missions/build-agent", nil))
	created := a.Request(http.MethodPost, "/api/v1/operations", fixtures.Object{"mission_name": "build-agent", "input": fixtures.Object{
		"instructions": "Create a fixture agent",
		"actions":      []fixtures.Object{{"name": "inspect", "description": "Inspect supplied input.", "input_schema": fixtures.Object{"type": "object"}, "output_schema": fixtures.Object{"type": "object"}}},
	}})
	buildOperation := fixtures.Decode[models.OperationResponse](t, created, http.StatusCreated)
	if buildOperation.Preflight == nil || len(buildOperation.Preflight.Actions) != 2 {
		t.Fatalf("build preflight included optional publication: %+v", buildOperation.Preflight)
	}
	for _, action := range buildOperation.Preflight.Actions {
		if !slices.Contains(build.RequiredActions, action.ActionName) {
			t.Fatalf("unscheduled optional action blocked build preflight: %+v", action)
		}
	}
	fixtures.Decode[models.OperationResponse](t, a.Request(http.MethodPost, fixtures.OperationPath(buildOperation.ID)+"/cancel", nil), http.StatusOK)
	path := "/api/v1/missions/analyze-text"
	mission := fixtures.Read[models.MissionResponse](a, path)
	index := slices.IndexFunc(catalog.Items, func(item models.MissionResponse) bool { return item.Name == mission.Name })
	if index < 0 || !reflect.DeepEqual(catalog.Items[index], mission) || len(mission.Objectives) == 0 || !slices.Equal(mission.RequiredActions, []string{"analyze-text", "write-summary"}) || !slices.Equal(mission.StartingActions, []string{"analyze-text"}) {
		t.Fatalf("mission catalog lost schemas or actions: %+v", mission)
	}
	if mission.Policies == nil || mission.Policies.Continuation == nil || !slices.Equal(mission.Policies.Continuation.AggregateResults, []string{"analyze-text"}) || !slices.Equal(mission.Policies.Continuation.ModelReview, []string{"write-summary"}) {
		t.Fatalf("mission catalog lost continuation policies: %+v", mission.Policies)
	}

	assertSchema(t, a, "Mission", a.Request(http.MethodGet, path, nil))
	fixtures.AssertError(t, a.Request(http.MethodGet, "/api/v1/missions/missing", nil), http.StatusNotFound, "mission_not_found")
	for range 2 {
		clean := a.Request(http.MethodPost, "/api/v1/testing/cleanup", nil)
		if clean.Status != http.StatusNoContent || clean.Body != "" {
			t.Fatalf("idempotent cleanup: %+v", clean)
		}
		if current := fixtures.Read[models.ListResponse[models.MissionResponse]](a, "/api/v1/missions"); !reflect.DeepEqual(current, catalog) {
			t.Fatal("cleanup deleted or changed the imported missions")
		}
	}
}

func TestMissionSharingPolicy(t *testing.T) {
	a := fixtures.Connect(t)
	definition := fixtures.SharingMission()
	path := "/api/v1/missions/" + definition.Name

	created := fixtures.Post[models.MissionResponse](a, "/api/v1/missions", definition, http.StatusCreated)
	assertMissionPolicies(t, created.Policies, definition.Policies)
	assertMissionPolicies(t, fixtures.Read[models.MissionResponse](a, path).Policies, definition.Policies)
	assertSchema(t, a, "Mission", a.Request(http.MethodGet, path, nil))
	first := fixtures.Post[models.OperationResponse](a, "/api/v1/operations", fixtures.Object{"mission_name": definition.Name, "input": fixtures.Object{}}, http.StatusCreated)
	if !reflect.DeepEqual(first.Mission.Policies, created.Policies) || len(first.Tasks) != 0 {
		t.Fatalf("operation lost policies or policies started work: %+v", first)
	}

	definition.Policies.Completion.DecisionBy = "model"
	definition.Policies.Continuation.AggregateResults = []string{"review-vulnerabilities"}
	definition.Policies.Continuation.ModelReview = []string{"review-vulnerabilities"}
	definition.Policies.Placement.SameAgent[1] = "review-vulnerabilities"
	definition.Policies.Sharing[0].ToActions[0] = "review-vulnerabilities"
	definition.Policies.Sharing[0].Include = []string{"decisions"}
	updated := fixtures.Decode[models.MissionResponse](t, a.Request(http.MethodPut, path, definition), http.StatusOK)
	assertMissionPolicies(t, updated.Policies, definition.Policies)
	assertMissionPolicies(t, fixtures.Read[models.MissionResponse](a, path).Policies, definition.Policies)
	retained := fixtures.Read[models.OperationResponse](a, fixtures.OperationPath(first.ID))
	if !reflect.DeepEqual(retained.Mission.Policies, created.Policies) {
		t.Fatal("mission update changed an existing operation's policies")
	}
	second := fixtures.Post[models.OperationResponse](a, "/api/v1/operations", fixtures.Object{"mission_name": definition.Name, "input": fixtures.Object{}}, http.StatusCreated)
	assertMissionPolicies(t, second.Mission.Policies, definition.Policies)

	for _, invalid := range fixtures.InvalidSharingPolicies() {
		candidate := fixtures.SharingMission()
		candidate.Policies.Sharing = invalid
		fixtures.AssertError(t, a.Request(http.MethodPost, "/api/v1/missions", candidate), http.StatusBadRequest, "invalid_request")
	}
	for _, placement := range fixtures.InvalidPlacementPolicies() {
		candidate := fixtures.PoliciesDefinition(definition, fixtures.Object{"placement": placement})
		fixtures.AssertError(t, a.Request(http.MethodPut, path, candidate), http.StatusBadRequest, "invalid_request")
	}
	for _, continuation := range fixtures.InvalidContinuationPolicies() {
		candidate := fixtures.PoliciesDefinition(definition, fixtures.Object{"continuation": continuation})
		fixtures.AssertError(t, a.Request(http.MethodPut, path, candidate), http.StatusBadRequest, "invalid_request")
	}
	for _, completion := range fixtures.InvalidCompletionPolicies() {
		candidate := fixtures.PoliciesDefinition(definition, fixtures.Object{"completion": completion})
		fixtures.AssertError(t, a.Request(http.MethodPut, path, candidate), http.StatusBadRequest, "invalid_request")
	}
	for _, completion := range []any{nil, fixtures.Object{}, fixtures.Object{"decision_by": "operator"}} {
		legacy := fixtures.PoliciesDefinition(definition, nil)
		legacy["completion"] = completion
		fixtures.AssertError(t, a.Request(http.MethodPut, path, legacy), http.StatusBadRequest, "invalid_request")
	}
	for _, rules := range []any{nil, fixtures.Object{}, fixtures.Object{"aggregate-results": fixtures.Object{"for_actions": []string{"find-vulnerabilities"}}}, fixtures.Object{"decide-next-actions": fixtures.Object{"for_actions": []string{"find-vulnerabilities"}, "params": fixtures.Object{"decision_by": "model"}}}, fixtures.Object{"same-agent": fixtures.Object{"for_actions": []string{"find-vulnerabilities"}}}} {
		legacy := fixtures.PoliciesDefinition(definition, nil)
		legacy["rules"] = rules
		fixtures.AssertError(t, a.Request(http.MethodPut, path, legacy), http.StatusBadRequest, "invalid_request")
	}
	for _, field := range []string{"context", "sharing"} {
		legacy := fixtures.PoliciesDefinition(definition, nil)
		delete(legacy, "policies")
		legacy[field] = definition.Policies.Sharing
		if field == "context" {
			legacy[field] = fixtures.Object{"sharing": definition.Policies.Sharing}
		}
		fixtures.AssertError(t, a.Request(http.MethodPut, path, legacy), http.StatusBadRequest, "invalid_request")
	}
	for _, policies := range []any{fixtures.Object{"completion": nil}, fixtures.Object{"sharing": []fixtures.Object{}}, fixtures.Object{"sharing": nil}, fixtures.Object{"continuation": nil}, fixtures.Object{"continuation": fixtures.Object{}}, fixtures.Object{"continuation": fixtures.Object{"aggregate_results": nil, "model_review": nil}}, fixtures.Object{"continuation": fixtures.Object{"aggregate_results": []string{}, "model_review": []string{}}}, fixtures.Object{"placement": nil}, fixtures.Object{"placement": fixtures.Object{}}, fixtures.Object{"placement": fixtures.Object{"same_agent": nil}}, fixtures.Object{"placement": fixtures.Object{"same_agent": []string{}}}, fixtures.Object{}, nil} {
		disabled := fixtures.Decode[models.MissionResponse](t, a.Request(http.MethodPut, path, fixtures.PoliciesDefinition(definition, policies)), http.StatusOK)
		if policies == nil {
			if disabled.Policies != nil {
				t.Fatal("null policies were not removed")
			}
		} else if disabled.Policies == nil || disabled.Policies.Completion != nil || len(disabled.Policies.Sharing) != 0 || disabled.Policies.Placement != nil && len(disabled.Policies.Placement.SameAgent) != 0 || disabled.Policies.Continuation != nil && (len(disabled.Policies.Continuation.AggregateResults) != 0 || len(disabled.Policies.Continuation.ModelReview) != 0) {
			t.Fatal("empty policies should declare no overrides")
		}
	}
	definition.Policies = nil
	disabled := fixtures.Decode[models.MissionResponse](t, a.Request(http.MethodPut, path, definition), http.StatusOK)
	if disabled.Policies != nil || fixtures.Read[models.MissionResponse](a, path).Policies != nil {
		t.Fatal("omitted policies were not removed")
	}
	for _, operation := range []models.OperationResponse{first, second} {
		fixtures.Decode[models.OperationResponse](t, a.Request(http.MethodPost, fixtures.OperationPath(operation.ID)+"/cancel", nil), http.StatusOK)
	}
}

func assertMissionPolicies(t *testing.T, got *models.MissionPoliciesResponse, want *models.MissionPoliciesRequest) {
	t.Helper()
	if got == nil || want == nil || len(got.Sharing) != len(want.Sharing) {
		t.Fatalf("mission sharing differs: got %+v, want %+v", got, want)
	}
	if (got.Completion == nil) != (want.Completion == nil) || got.Completion != nil && got.Completion.DecisionBy != want.Completion.DecisionBy {
		t.Fatalf("mission completion differs: got %+v, want %+v", got.Completion, want.Completion)
	}
	if (got.Continuation == nil) != (want.Continuation == nil) || got.Continuation != nil && (!slices.Equal(got.Continuation.AggregateResults, want.Continuation.AggregateResults) || !slices.Equal(got.Continuation.ModelReview, want.Continuation.ModelReview)) {
		t.Fatalf("mission continuation differs: got %+v, want %+v", got.Continuation, want.Continuation)
	}
	if (got.Placement == nil) != (want.Placement == nil) || got.Placement != nil && !slices.Equal(got.Placement.SameAgent, want.Placement.SameAgent) {
		t.Fatalf("mission placement differs: got %+v, want %+v", got.Placement, want.Placement)
	}
	for i, route := range got.Sharing {
		expected := want.Sharing[i]
		if !slices.Equal(route.FromActions, expected.FromActions) || !slices.Equal(route.ToActions, expected.ToActions) || !slices.Equal(route.Include, expected.Include) {
			t.Fatalf("mission sharing route %d differs: got %+v, want %+v", i, route, expected)
		}
	}
}

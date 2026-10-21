package default_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/defos-ai/commander/apps/controllers/default/data"
	"github.com/defos-ai/commander/framework/domain/missions"
)

func TestMissionImportValidatesBeforeUpdatingAndPreservesOtherMissions(t *testing.T) {
	store := missionStore{
		"review": {Name: "review", Objective: "Old objective"},
		"other":  {Name: "other", Objective: "Keep this mission"},
	}
	before := map[string]missions.Mission{"review": store["review"], "other": store["other"]}
	path := filepath.Join(t.TempDir(), "missions.json")
	valid := `{"name":"review","objective":"New objective","input_schema":{},"output_schema":{}}`
	for _, invalid := range []string{
		`{"name":"broken"}`,
		`{"name":"broken","objective":"Invalid rule","input_schema":{},"output_schema":{},"rules":{"decide-next-actions":{"for_actions":["inspect"],"params":{"decision_by":"unknown"}}}}`,
	} {
		if err := os.WriteFile(path, []byte("["+valid+","+invalid+"]"), 0600); err != nil {
			t.Fatal(err)
		}
		err := data.UpsertMissions(t.Context(), store, path)
		if err == nil || !reflect.DeepEqual(map[string]missions.Mission(store), before) {
			t.Fatalf("invalid import changed catalog: %+v, error=%v", store, err)
		}
	}

	if err := os.WriteFile(path, []byte("["+valid+"]"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := data.UpsertMissions(t.Context(), store, path); err != nil {
			t.Fatal(err)
		}
	}

	if len(store) != 2 || store["review"].Objective != "New objective" || !reflect.DeepEqual(store["other"], before["other"]) {
		t.Fatalf("unexpected catalog after import: %+v", store)
	}
}

func TestWebsiteMissionCollectsAutomaticChecksBeforeModelReview(t *testing.T) {
	websites := defaultMissions(t)
	for _, test := range []struct {
		name      string
		discovery string
		checks    []string
	}{
		{"review-website-no-browser", "discover-pages-no-browser", []string{"check-links", "check-seo", "detect-technologies"}},
		{"review-website-with-browser", "discover-pages", []string{"check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			website := websites[test.name]
			aggregate := website.Rules[missions.AggregateResults]
			decision := website.Rules[missions.DecideNextActions]
			required := append([]string{test.discovery}, test.checks...)
			if !slices.Equal(website.StartingActions, []string{test.discovery}) || !slices.Equal(website.RequiredActions, required) || !slices.Equal(aggregate.ForActions, []string{test.discovery}) {
				t.Fatalf("website checks are not grouped automatic work: %+v", website)
			}
			if len(website.Rules) != 2 {
				t.Fatalf("unexpected mission rules: %+v", website.Rules)
			}
			if !slices.Equal(decision.ForActions, test.checks) || decision.Params == nil || decision.Params.DecisionBy != missions.Model {
				t.Fatalf("website checks are not followed by model review: %+v", decision)
			}
		})
	}
}

func TestInstallAgentMissionNeedsOnlySpawnerAndReviewsReadiness(t *testing.T) {
	mission := defaultMissions(t)["install-agent"]
	if !slices.Equal(mission.RequiredActions, []string{"spawn-agent"}) || !slices.Equal(mission.StartingActions, []string{"spawn-agent"}) || len(mission.Steps) != 0 {
		t.Fatalf("agent installation must work before business agents register: %+v", mission)
	}
	decision := mission.Rules[missions.DecideNextActions]
	if len(mission.Rules) != 1 || !slices.Equal(decision.ForActions, []string{"spawn-agent"}) || decision.Params == nil || decision.Params.DecisionBy != missions.Model {
		t.Fatalf("spawn completion must trigger result and readiness review: %+v", mission.Rules)
	}
	for _, input := range []string{
		`{"package":"github.com/defos-ai/commander-web-agent@v0.2.0"}`,
		`{"package":"https://github.com/defos-ai/commander-web-agent"}`,
		`{"package":"github.com/defos-ai/commander-web-agent"}`,
		`{"package":"github.com/defos-ai/commander/apps/agents/builder@stable"}`,
		`{"package":"https://github.com/example/private-agent","visibility":"private"}`,
		`{"package":"github.com/defos-ai/commander/apps/agents/builder@v0.2.0"}`,
		`{"package":"github.com/defos-ai/commander-web-agent@v0.2.0","visibility":"public"}`,
		`{"package":"github.com/example/private-agent@v0.2.0","visibility":"private"}`,
	} {
		if err := mission.ValidateInput(json.RawMessage(input)); err != nil {
			t.Fatalf("package rejected: %v", err)
		}
		if err := mission.ValidateOutput(json.RawMessage(input), json.RawMessage(`{"agent_id":"web-agent-1234abcd","limitations":["Actions still need configuration"]}`)); err != nil {
			t.Fatalf("registration with readiness limitations rejected: %v", err)
		}
	}
	for _, input := range []string{
		`{}`,
		`{"package":"github.com/defos-ai/commander-web-agent@latest"}`,
		`{"package":"github.com/defos-ai/commander-web-agent@v0.2.0","environment":{"KEY":"value"}}`,
		`{"package":"github.com/example/private-agent@v0.2.0","visibility":"internal"}`,
		`{"package":"github.com/example/private-agent@v0.2.0","visibility":""}`,
	} {
		if err := mission.ValidateInput(json.RawMessage(input)); err == nil {
			t.Fatalf("invalid installation input accepted: %s", input)
		}
	}
}

func TestBuildAgentMissionKeepsWorkspaceAffinityAndOptionalPublicationAndRelease(t *testing.T) {
	catalog := defaultMissions(t)
	if _, exists := catalog["publish-agent"]; exists {
		t.Fatal("source publication must remain an optional action in build-agent")
	}
	if _, exists := catalog["release-agent"]; exists {
		t.Fatal("release must remain an optional action in build-agent")
	}
	mission := catalog["build-agent"]
	stages := []string{"checkout-template", "build-agent"}
	if !slices.Equal(mission.RequiredActions, stages) || !slices.Equal(mission.OptionalActions, []string{"publish-agent", "release-agent"}) || !slices.Equal(mission.StartingActions, stages[:1]) {
		t.Fatalf("build mission lost its checkout prerequisite: %+v", mission)
	}
	affinity, decision := mission.Rules[missions.SameAgent], mission.Rules[missions.DecideNextActions]
	if len(mission.Rules) != 2 || !slices.Equal(affinity.ForActions, stages) || !slices.Equal(decision.ForActions, []string{"checkout-template", "build-agent", "publish-agent"}) || decision.Params == nil || decision.Params.DecisionBy != missions.Model {
		t.Fatalf("workspace stages need affinity and build/publication review; release polls must continue automatically: %+v", mission.Rules)
	}
	input := json.RawMessage(`{"instructions":"Create a JSON validation agent."}`)
	if err := mission.ValidateInput(input); err != nil {
		t.Fatal(err)
	}
	if err := mission.ValidateOutput(input, json.RawMessage(`{"workspace_id":"checkout-example","summary":"Generated the agent and validated its source."}`)); err != nil {
		t.Fatal(err)
	}
	publicationInput := json.RawMessage(`{"instructions":"Create a JSON validation agent.","repository":"example/json-agent","visibility":"public"}`)
	if err := mission.ValidateInput(publicationInput); err != nil {
		t.Fatalf("optional publication preferences rejected: %v", err)
	}
	publicationOutput := json.RawMessage(`{"workspace_id":"checkout-example","summary":"Generated and published the source.","publication":{"repository_url":"https://github.com/example/json-agent","commit_url":"https://github.com/example/json-agent/commit/0123456789abcdef0123456789abcdef01234567","commit_sha":"0123456789abcdef0123456789abcdef01234567","visibility":"public","branch":"main"}}`)
	if err := mission.ValidateOutput(publicationInput, publicationOutput); err != nil {
		t.Fatalf("publication result rejected: %v", err)
	}
	if err := mission.ValidateOutput(publicationInput, json.RawMessage(`{"workspace_id":"checkout-example","summary":"Generated the source without publishing."}`)); err != nil {
		t.Fatalf("publication must remain optional in the result: %v", err)
	}
	if err := mission.ValidateOutput(input, json.RawMessage(`{"workspace_id":"checkout-example","summary":"Generated the source.","publication":{"repository_url":"https://github.com/example/json-agent"}}`)); err == nil {
		t.Fatal("incomplete publication result accepted")
	}
	releaseInput := json.RawMessage(`{"instructions":"Create a JSON validation agent.","repository":"example/json-agent","visibility":"public","release_version":"v0.1.0"}`)
	if err := mission.ValidateInput(releaseInput); err != nil {
		t.Fatalf("explicit optional release request rejected: %v", err)
	}
	releaseOutput := json.RawMessage(`{"workspace_id":"checkout-example","summary":"Released the reviewed source.","release":{"repository":"example/json-agent","commit_sha":"0123456789abcdef0123456789abcdef01234567","version":"v0.1.0","agent_type":"json-agent","visibility":"public","status":"released","workflow_run_id":123,"workflow_url":"https://github.com/example/json-agent/actions/runs/123","conclusion":"success","package":"github.com/example/json-agent@v0.1.0","release_url":"https://github.com/example/json-agent/releases/tag/v0.1.0"}}`)
	if err := mission.ValidateOutput(releaseInput, releaseOutput); err != nil {
		t.Fatalf("verified release result rejected: %v", err)
	}
	if err := mission.ValidateOutput(releaseInput, json.RawMessage(`{"workspace_id":"checkout-example","summary":"Build finished; publication and release were skipped."}`)); err != nil {
		t.Fatalf("release must remain optional in the result: %v", err)
	}
	if err := mission.ValidateOutput(releaseInput, json.RawMessage(`{"workspace_id":"checkout-example","summary":"Still waiting.","release":{"repository":"example/json-agent","commit_sha":"0123456789abcdef0123456789abcdef01234567","version":"v0.1.0","agent_type":"json-agent","visibility":"public","status":"pending"}}`)); err == nil {
		t.Fatal("pending release accepted as a finished mission report")
	}
	for _, invalid := range []string{`{}`, `{"instructions":" "}`, `{"instructions":"Build an agent","CODEX_API_KEY":"value"}`, `{"instructions":"Build an agent","repository":"example/json-agent","visibility":"internal"}`, `{"instructions":"Build an agent","release_version":"stable"}`, `{"instructions":"Build an agent","release_version":"v0.1.0-rc.1"}`} {
		if err := mission.ValidateInput(json.RawMessage(invalid)); err == nil {
			t.Fatalf("invalid build input accepted: %s", invalid)
		}
	}
}

func defaultMissions(t *testing.T) map[string]missions.Mission {
	t.Helper()
	var definitions []json.RawMessage
	if err := json.Unmarshal(data.DefaultMissions, &definitions); err != nil {
		t.Fatal(err)
	}
	websites := map[string]missions.Mission{}
	for _, raw := range definitions {
		mission, err := missions.LoadMission(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := websites[mission.Name]; exists {
			t.Fatalf("duplicate mission %q", mission.Name)
		}
		websites[mission.Name] = mission
	}
	return websites
}

type missionStore map[string]missions.Mission

func (missionStore) Create(context.Context, missions.Mission) error {
	return errors.New("mission import must use Upsert")
}

func (s missionStore) Upsert(_ context.Context, mission missions.Mission) error {
	s[mission.Name] = mission.Clone()
	return nil
}
func (s missionStore) Get(_ context.Context, name string) (missions.Mission, error) {
	mission, ok := s[name]
	if !ok {
		return missions.Mission{}, missions.ErrUnknownMission
	}
	return mission.Clone(), nil
}
func (s missionStore) List(context.Context) ([]missions.Mission, error) {
	var result []missions.Mission
	for _, mission := range s {
		result = append(result, mission.Clone())
	}
	return result, nil
}

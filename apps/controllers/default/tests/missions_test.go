package default_test

import (
	"context"
	"encoding/json"
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

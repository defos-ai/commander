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
	if err := os.WriteFile(path, []byte("["+valid+`,{"name":"broken"}]`), 0600); err != nil {
		t.Fatal(err)
	}

	err := data.UpsertMissions(t.Context(), store, path)

	if err == nil || !reflect.DeepEqual(map[string]missions.Mission(store), before) {
		t.Fatalf("invalid import changed catalog: %+v, error=%v", store, err)
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
	if len(websites) != 2 {
		t.Fatalf("expected two website review missions, got %d", len(websites))
	}
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
			workflow := website.Automations[test.discovery]
			required := append([]string{test.discovery}, test.checks...)
			if !slices.Equal(website.StartingActions, []string{test.discovery}) || !slices.Equal(website.RequiredActions, required) || !slices.Equal(workflow.Run, test.checks) || workflow.Continue != missions.WhenAll {
				t.Fatalf("website checks are not grouped automatic work: %+v", website)
			}
			if len(website.Automations) != len(test.checks)+1 {
				t.Fatalf("unexpected automation rules: %+v", website.Automations)
			}
			for _, name := range test.checks {
				if website.Automations[name].Decide != missions.Model {
					t.Errorf("%s is not followed by model review", name)
				}
			}
		})
	}
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

package default_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"commander/apps/controllers/default/data"
	"commander/framework/domain/missions"
)

func TestMissionImportValidatesBeforeUpdatingAndPreservesOtherMissions(t *testing.T) {
	// Arrange: a catalog has both a mission being updated and an unrelated one.
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

	// Act: the second definition is invalid, so the first must not be upserted.
	err := data.UpsertMissions(t.Context(), store, path)

	// Assert: invalid input leaves the entire catalog untouched.
	if err == nil || !reflect.DeepEqual(map[string]missions.Mission(store), before) {
		t.Fatalf("invalid import changed catalog: %+v, error=%v", store, err)
	}

	// Act: import the corrected file twice, updating by name.
	if err := os.WriteFile(path, []byte("["+valid+"]"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := data.UpsertMissions(t.Context(), store, path); err != nil {
			t.Fatal(err)
		}
	}

	// Assert: repeated startup updates the intended mission and preserves the other.
	if len(store) != 2 || store["review"].Objective != "New objective" || !reflect.DeepEqual(store["other"], before["other"]) {
		t.Fatalf("unexpected catalog after import: %+v", store)
	}
}

func TestWebsiteMissionCollectsAutomaticChecksBeforeModelReview(t *testing.T) {
	// Arrange: validate the mission bytes actually bundled in the app.
	var definitions []json.RawMessage
	if err := json.Unmarshal(data.DefaultMissions, &definitions); err != nil {
		t.Fatal(err)
	}
	var website missions.Mission
	for _, raw := range definitions {
		mission, err := missions.LoadMission(raw)
		if err != nil {
			t.Fatal(err)
		}
		if mission.Name == "review-website" {
			website = mission
		}
	}

	// Act: inspect the automatic branch that follows website discovery.
	workflow := website.Automations["discover-pages"]

	// Assert: discovery runs grouped checks; the model reviews their collected results.
	if website.Name == "" || !slices.Equal(website.StartingActions, []string{"discover-pages"}) || workflow.Continue != missions.WhenAll || len(workflow.Run) == 0 {
		t.Fatalf("website discovery is not grouped automatic work: %+v", website)
	}
	for _, name := range []string{"check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"} {
		if !slices.Contains(workflow.Run, name) || !slices.Contains(website.RequiredActions, name) || website.Automations[name].Decide != missions.Model {
			t.Errorf("%s is not an automatic check followed by model review", name)
		}
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

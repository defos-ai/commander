package missions_test

import (
	"encoding/json"
	"testing"

	"commander/framework/domain/missions"
)

func TestMissionAutomationJSONAndSnapshot(t *testing.T) {
	// Arrange: the persisted definition also serves as the external mission contract.
	data := []byte(`{
  "name":"scan", "objective":"Inspect a target", "input_schema":{}, "output_schema":{},
  "automations":{
   "dns":{"run":["prepare"],"continue":"when_each"},
   "prepare":{"run":["scan"],"continue":"when_all"},
   "scan":{"decide":"model"}
  }
 }`)
	// Act: load, snapshot, and edit the original definition.
	mission, err := missions.LoadMission(data)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := mission.Clone()
	mission.Automations["dns"].Run[0] = "another-action"
	delete(mission.Automations, "scan")
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := missions.LoadMission(encoded)
	if err != nil {
		t.Fatal(err)
	}
	// Assert: the snapshot is independent, serializable, and retains both policies.
	if restored.Automations["dns"].Run[0] != "prepare" || restored.Automations["dns"].Continue != missions.WhenEach || restored.Automations["prepare"].Continue != missions.WhenAll || restored.Automations["scan"].Decide != missions.Model {
		t.Fatalf("automation lost or changed: %s", encoded)
	}
}

func TestMissionRejectsInvalidAutomation(t *testing.T) {
	for name, rule := range map[string]missions.Automation{
		"empty":                         {},
		"conflicting":                   {Run: []string{"scan"}, Decide: missions.Model},
		"invalid continuation":          {Run: []string{"scan"}, Continue: "unknown"},
		"invalid decision":              {Decide: "user"},
		"blank action":                  {Run: []string{" "}},
		"model cannot collect new work": {Decide: missions.Model, Continue: missions.WhenAll},
		" ":                             {Run: []string{"scan"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange.
			mission := missions.Mission{Name: "scan", Objective: "Inspect", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`), Automations: map[string]missions.Automation{name: rule}}
			encoded, err := json.Marshal(mission)
			if err != nil {
				t.Fatal(err)
			}
			// Act.
			_, err = missions.LoadMission(encoded)
			// Assert.
			if err == nil {
				t.Fatal("invalid automation accepted")
			}
		})
	}
}

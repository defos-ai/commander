package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"commander/framework/domain/missions"
)

func TestControllerLeavesMissionsUnchangedByDefault(t *testing.T) {
	for _, empty := range []bool{true, false} {
		name := "existing catalog"
		if empty {
			name = "empty catalog"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange: a database with either no missions or an existing definition.
			d := arrange(t)
			if empty {
				if err := os.WriteFile(filepath.Join(d.root, "database", "missions.json"), []byte(`[]`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := d.database.MissionStore.List(d.ctx)
			if err != nil {
				t.Fatal(err)
			}

			// Act: start without --with-missions.
			controller := d.start(t, false)
			after, err := d.database.MissionStore.List(d.ctx)
			if err != nil {
				t.Fatal(err)
			}

			// Assert: startup adds or changes no definitions.
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("startup changed missions: got %+v, want %+v", after, before)
			}
			controller.stop(t)
		})
	}
}

func TestControllerUpsertsMissionsFromFile(t *testing.T) {
	// Arrange: a custom mission and a separate mission file in the working directory.
	d := arrange(t)
	definitions, err := os.ReadFile("../data/default-missions.json")
	if err != nil {
		t.Fatal(err)
	}
	missionFile := filepath.Join(d.root, "missions.json")
	if err := os.WriteFile(missionFile, definitions, 0600); err != nil {
		t.Fatal(err)
	}
	custom, err := d.database.MissionStore.Get(d.ctx, "review")
	if err != nil {
		t.Fatal(err)
	}

	// Act: load the supplied file using a relative path.
	controller := d.start(t, false, "--with-missions", "./missions.json")
	catalog, err := d.database.MissionStore.List(d.ctx)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := d.database.MissionStore.Get(d.ctx, "review-website")
	if err != nil {
		t.Fatal(err)
	}
	storedCustom, err := d.database.MissionStore.Get(d.ctx, custom.Name)
	if err != nil {
		t.Fatal(err)
	}

	// Assert: startup installs usable defaults and preserves custom definitions.
	if definition.Automations["discover-pages"].Continue != missions.WhenAll ||
		!reflect.DeepEqual(definition.Automations["discover-pages"].Run, []string{"check-links", "check-accessibility", "capture-page", "check-seo", "detect-technologies"}) {
		t.Fatalf("missing bundled automation: %+v", definition)
	}
	for _, name := range definition.Automations["discover-pages"].Run {
		if definition.Automations[name].Decide != missions.Model {
			t.Fatalf("%s is missing its grouped model review: %+v", name, definition.Automations[name])
		}
	}
	if err := definition.ValidateInput([]byte(`{"url":"http://sample-website:8080/"}`)); err != nil {
		t.Fatalf("invalid bundled input schema: %v", err)
	}
	if !reflect.DeepEqual(storedCustom, custom) {
		t.Fatalf("startup changed custom mission: %+v", storedCustom)
	}
	controller.stop(t)

	// Arrange: edit the input file without rebuilding the controller.
	definition.Objective = "Updated objective from file"
	definitions, err = json.Marshal([]missions.Mission{definition})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(missionFile, definitions, 0600); err != nil {
		t.Fatal(err)
	}
	for i := range catalog {
		if catalog[i].Name == definition.Name {
			catalog[i] = definition
		}
	}

	// Act: restart with the same database and the updated input file.
	controller = d.start(t, false, "--with-missions", missionFile)
	restored, err := d.database.MissionStore.List(d.ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Assert: defaults are refreshed without duplicates or changes to custom missions.
	if !reflect.DeepEqual(restored, catalog) {
		t.Fatalf("restart changed catalog: got %+v, want %+v", restored, catalog)
	}
	controller.stop(t)
}

func TestControllerRejectsInvalidMissionFiles(t *testing.T) {
	for _, scenario := range []struct{ name, contents string }{
		{"missing file", ""},
		{"invalid JSON", `[{`},
		{"null catalog", `null`},
		{"invalid definition", `[
			{"name":"new-mission","objective":"Valid first mission","input_schema":{},"output_schema":{}},
			{"name":"invalid-mission"}
		]`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: supply a missing or invalid file alongside an existing catalog.
			d := arrange(t)
			before, err := d.database.MissionStore.List(d.ctx)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(d.root, "input-missions.json")
			if scenario.contents != "" {
				if err := os.WriteFile(path, []byte(scenario.contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(d.ctx, 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "--with-missions", path)
			command.Dir = d.root
			for key, value := range d.env {
				command.Env = append(command.Env, key+"="+value)
			}

			// Act: launch the controller with the invalid file.
			output, err := command.CombinedOutput()

			// Assert: startup fails before readiness or any mission writes.
			if err == nil || ctx.Err() != nil || !bytes.Contains(output, []byte("with-missions:")) || bytes.Contains(output, []byte("Controller ready")) {
				t.Fatalf("unexpected startup outcome: %v\n%s", err, output)
			}
			after, err := d.database.MissionStore.List(d.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid file changed missions: got %+v, want %+v", after, before)
			}
		})
	}
}

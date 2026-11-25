package missiontest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/defos-ai/commander/framework/domain/missions"
)

// LoadMissions imports an object or array from a mission file without replacing
// existing definitions. Shared fixtures should import each definition once.
func (d *Driver) LoadMissions(path string) []missions.Mission {
	d.t.Helper()
	file, err := os.Open(path)
	if err != nil {
		d.t.Fatal("missiontest: could not open mission file")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxJSONBytes+1))
	if err != nil || len(raw) > maxJSONBytes {
		d.t.Fatal("missiontest: could not read bounded mission JSON")
	}
	raw = bytes.TrimSpace(raw)
	var entries []json.RawMessage
	if len(raw) > 0 && raw[0] == '[' {
		err = json.Unmarshal(raw, &entries)
	} else {
		entries = []json.RawMessage{raw}
	}
	if err != nil || len(entries) == 0 {
		d.t.Fatal("missiontest: expected a mission object or a nonempty mission array")
	}
	definitions := make([]missions.Mission, len(entries))
	seen := make(map[string]bool, len(entries))
	for i, entry := range entries {
		definition, err := missions.LoadMission(entry)
		if err != nil {
			d.t.Fatalf("missiontest: mission definition %d has invalid schemas or unsupported fields", i+1)
		}
		if seen[definition.Name] {
			d.t.Fatal("missiontest: mission file contains duplicate names")
		}
		seen[definition.Name], definitions[i] = true, definition
	}
	for i, definition := range definitions {
		definitions[i] = d.ImportMission(definition)
	}
	return definitions
}

func (d *Driver) ImportMission(definition missions.Mission) missions.Mission {
	d.t.Helper()
	if definition.Validate() != nil {
		d.t.Fatal("missiontest: invalid mission definition")
	}
	imported := read[missions.Mission](d, http.MethodPost, "/api/v1/missions", definition, http.StatusCreated)
	if imported.Name != definition.Name || imported.Validate() != nil {
		d.t.Fatal("missiontest: API did not return the imported mission")
	}
	return imported
}

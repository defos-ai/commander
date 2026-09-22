package data

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"commander/framework/domain/missions"
)

// Missions not present in the file are preserved.
func UpsertMissions(ctx context.Context, store missions.MissionStore, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read missions: %w", err)
	}
	var definitions []json.RawMessage
	if err := json.Unmarshal(data, &definitions); err != nil {
		return fmt.Errorf("decode missions: %w", err)
	}
	if definitions == nil {
		return fmt.Errorf("missions file must contain a JSON array")
	}
	loaded := make([]missions.Mission, 0, len(definitions))
	for i, definition := range definitions {
		mission, err := missions.LoadMission(definition)
		if err != nil {
			return fmt.Errorf("load mission %d: %w", i+1, err)
		}
		loaded = append(loaded, mission)
	}
	for _, mission := range loaded {
		if err := store.Upsert(ctx, mission); err != nil {
			return fmt.Errorf("upsert mission %q: %w", mission.Name, err)
		}
	}
	return nil
}

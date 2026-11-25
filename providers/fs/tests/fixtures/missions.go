package fixtures

import (
	"encoding/json"
	"testing"

	"github.com/defos-ai/commander/framework/domain/missions"
	missionfs "github.com/defos-ai/commander/providers/fs/missions"
)

func OpenMissionStore(t *testing.T, root string) *missionfs.MissionFsStore {
	t.Helper()
	store, err := missionfs.NewMissionFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func Mission(name string) missions.Mission {
	return missions.Mission{
		Name: name, Objectives: []string{"Review the supplied input"},
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

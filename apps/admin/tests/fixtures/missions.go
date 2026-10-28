package fixtures

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/apps/admin/tests/fakes"
)

type MissionAPI struct {
	*fakes.MissionAPI
	connection client.Connection
}

func NewMissionAPI(t testing.TB, config fakes.MissionAPIConfig) *MissionAPI {
	t.Helper()
	api := fakes.NewMissionAPI(t, config)
	return &MissionAPI{MissionAPI: api, connection: StartAPI(t, api)}
}

func (f *MissionAPI) Connection() client.Connection { return f.connection }

func MissionJSON(name, objective string) string {
	encodedName, _ := json.Marshal(name)
	encodedObjective, _ := json.Marshal(objective)
	return fmt.Sprintf(`{"name":%s,"objectives":[%s],"required_actions":["review"],"starting_actions":["review"],"input_schema":{"type":"object","x-precise":9007199254740993,"x-exponent":1e+09},"output_schema":{}}`, encodedName, encodedObjective)
}

func MissionsFile(t testing.TB, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "missions.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

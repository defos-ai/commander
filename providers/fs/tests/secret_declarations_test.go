package providers_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	actionfs "github.com/defos-ai/commander/providers/fs/actions"
)

func TestSecretDeclarationsSurviveCatalogAndInventoryRestart(t *testing.T) {
	root := t.TempDir()
	catalog, err := actionfs.NewActionFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	action := actions.Action{
		Name: "build-agent", Status: actions.Unknown, RequiredSecrets: []string{"MODEL_API_KEY", "GITHUB_TOKEN"},
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
	}
	if err := catalog.Upsert(t.Context(), action); err != nil {
		t.Fatal(err)
	}
	catalog, err = actionfs.NewActionFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	storedAction, err := catalog.Get(t.Context(), action.Name)
	if err != nil || !reflect.DeepEqual(storedAction, action) {
		t.Fatalf("reopened action changed declarations: %#v, %v", storedAction, err)
	}
	storedAction.RequiredSecrets[0] = "changed"
	items, err := catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []actions.Action{action}) {
		t.Fatalf("catalog reader changed persisted declarations: %#v, %v", items, err)
	}

	inventory := openAgentFsStore(t, root)
	record := fsInventoryAgent(1, time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	record.Actions = []actions.Action{action}
	if err := inventory.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	inventory = openAgentFsStore(t, root)
	stored, err := inventory.Get(t.Context(), record.ID)
	if err != nil || !reflect.DeepEqual(stored, record) {
		t.Fatalf("reopened agent changed declarations: %#v, %v", stored, err)
	}
	stored.Actions[0].RequiredSecrets[0] = "changed"
	listed, err := inventory.List(t.Context())
	if err != nil || !reflect.DeepEqual(listed, []agents.Agent{record}) {
		t.Fatalf("inventory reader changed persisted declarations: %#v, %v", listed, err)
	}
}

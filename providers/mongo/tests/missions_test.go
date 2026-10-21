package mongo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/domain/missions"
	mongomissions "github.com/defos-ai/commander/providers/mongo/missions"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMissionMongoStore(t *testing.T) {
	database := openStore(t, "missions")
	catalog := mongomissions.NewMissionMongoStore(database)
	ctx := t.Context()
	original := missions.Mission{
		Name: "alpha", Objective: "Review a page",
		RequiredActions: []string{"discover", "check"}, OptionalActions: []string{"publish"}, StartingActions: []string{"discover"},
		Steps: []missions.Step{{ID: "review", Objective: "Review findings"}},
		Rules: missions.RuleSet{
			missions.AggregateResults:  {ForActions: []string{"discover"}},
			missions.DecideNextActions: {ForActions: []string{"check"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
		},
		InputSchema:  json.RawMessage(`{"type":"object","x-example":9007199254740993,"properties":{"url":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}}}`),
	}
	other := original.Clone()
	other.Name = "beta"
	items, err := catalog.List(ctx)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty catalog = %#v, error = %v", items, err)
	}
	if _, err := catalog.Get(ctx, "missing"); !errors.Is(err, missions.ErrUnknownMission) {
		t.Fatalf("missing mission: %v", err)
	}

	for _, mission := range []missions.Mission{other, original} {
		if err := catalog.Upsert(ctx, mission); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := catalog.Get(ctx, original.Name)
	if err != nil || !reflect.DeepEqual(stored, original) {
		t.Fatalf("mission changed on round trip: %#v, error = %v", stored, err)
	}
	items, err = catalog.List(ctx)
	if err != nil || len(items) != 2 || items[0].Name != "alpha" || items[1].Name != "beta" {
		t.Fatalf("catalog ordering: %#v, error = %v", items, err)
	}
	var document bson.Raw
	if err := database.GetByID(ctx, "missions", original.Name, &document); err != nil {
		t.Fatal(err)
	}
	_, schema := document.Lookup("input_schema").Binary()
	if !bytes.Equal(schema, original.InputSchema) || document.Lookup("name").StringValue() != original.Name {
		t.Fatal("provider changed JSON schema precision or stored field names")
	}
	stored.RequiredActions[0] = "modified by reader"
	stored.OptionalActions[0] = "modified by reader"
	stored.InputSchema[0] = '!'
	stored.Rules[missions.AggregateResults].ForActions[0] = "modified by reader"
	stored.Rules[missions.DecideNextActions].Params.DecisionBy = "modified by reader"
	unchanged, err := catalog.Get(ctx, original.Name)
	if err != nil || !reflect.DeepEqual(unchanged, original) {
		t.Fatalf("caller changed persisted mission: %#v, error = %v", unchanged, err)
	}

	updated := original.Clone()
	updated.Objective = "Review before release"
	updated.OptionalActions, updated.Steps, updated.Rules = nil, nil, nil
	if err := catalog.Upsert(ctx, updated); err != nil {
		t.Fatal(err)
	}
	stored, err = catalog.Get(ctx, updated.Name)
	if err != nil || !reflect.DeepEqual(stored, updated) {
		t.Fatalf("upsert retained old fields: %#v, error = %v", stored, err)
	}
	invalid := updated.Clone()
	invalid.InputSchema = json.RawMessage(`{"type":"invalid-type"}`)
	if err := catalog.Upsert(ctx, invalid); err == nil {
		t.Fatal("invalid mission replaced a valid one")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := catalog.Upsert(cancelled, original); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upsert: %v", err)
	}
	stored, err = catalog.Get(ctx, original.Name)
	if err != nil || !reflect.DeepEqual(stored, updated) {
		t.Fatalf("rejected writes changed the catalog: %#v, error = %v", stored, err)
	}
	items, err = catalog.List(ctx)
	if err != nil || len(items) != 2 || !reflect.DeepEqual(items[1], other) {
		t.Fatalf("upsert overwrote unrelated missions: %#v, error = %v", items, err)
	}
}

func TestConcurrentMissionUpserts(t *testing.T) {
	database := openStore(t, "missions")
	catalog := mongomissions.NewMissionMongoStore(database)
	const writers = 12
	completed := make(chan error, writers)

	for i := range writers {
		go func() {
			mission := missions.Mission{Name: "shared", Objective: fmt.Sprintf("Revision %d", i), InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
			if err := catalog.Upsert(t.Context(), mission); err != nil {
				completed <- err
				return
			}
			mission.Name = fmt.Sprintf("mission-%02d", i)
			completed <- catalog.Upsert(t.Context(), mission)
		}()
	}
	for range writers {
		if err := <-completed; err != nil {
			t.Error(err)
		}
	}

	items, err := catalog.List(t.Context())
	if err != nil || len(items) != writers+1 {
		t.Fatalf("concurrent writes lost or duplicated missions: count=%d, error=%v", len(items), err)
	}
	for _, mission := range items {
		if err := mission.Validate(); err != nil {
			t.Fatalf("concurrent writes produced invalid mission: %v", err)
		}
	}
}

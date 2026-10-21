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
		Name: "alpha", Objectives: []string{"Review a page", "Summarize findings"},
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
		if err := catalog.Create(ctx, mission); err != nil {
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
	stored.Objectives[0] = "modified by reader"
	stored.RequiredActions[0] = "modified by reader"
	stored.OptionalActions[0] = "modified by reader"
	stored.InputSchema[0] = '!'
	stored.Rules[missions.AggregateResults].ForActions[0] = "modified by reader"
	stored.Rules[missions.DecideNextActions].Params.DecisionBy = "modified by reader"
	unchanged, err := catalog.Get(ctx, original.Name)
	if err != nil || !reflect.DeepEqual(unchanged, original) {
		t.Fatalf("caller changed persisted mission: %#v, error = %v", unchanged, err)
	}
	conflicting := original.Clone()
	conflicting.Objectives = []string{"Replace the existing mission"}
	for _, duplicate := range []missions.Mission{original, conflicting} {
		if err := catalog.Create(ctx, duplicate); !errors.Is(err, missions.ErrMissionExists) {
			t.Fatalf("duplicate create = %v, want ErrMissionExists", err)
		}
	}
	unchanged, err = catalog.Get(ctx, original.Name)
	if err != nil || !reflect.DeepEqual(unchanged, original) {
		t.Fatalf("duplicate create changed the original: %#v, error = %v", unchanged, err)
	}

	updated := original.Clone()
	updated.Objectives = []string{"Review before release"}
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
	invalid.Name = "invalid"
	if err := catalog.Create(ctx, invalid); err == nil {
		t.Fatal("invalid mission was created")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := catalog.Upsert(cancelled, original); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upsert: %v", err)
	}
	newMission := original.Clone()
	newMission.Name = "cancelled"
	if err := catalog.Create(cancelled, newMission); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create: %v", err)
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

func TestConcurrentMissionCreates(t *testing.T) {
	catalogs := []*mongomissions.MissionMongoStore{
		mongomissions.NewMissionMongoStore(openStore(t, "missions")),
		mongomissions.NewMissionMongoStore(openStore(t, "missions")),
	}
	const writers = 12
	type result struct {
		mission missions.Mission
		err     error
	}
	completed := make(chan result, writers)
	start := make(chan struct{})
	for i := range writers {
		go func() {
			<-start
			mission := missions.Mission{Name: "shared", Objectives: []string{fmt.Sprintf("Creator %d", i)}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
			completed <- result{mission, catalogs[i%len(catalogs)].Create(t.Context(), mission)}
		}()
	}
	close(start)
	winners := 0
	var winner missions.Mission
	for range writers {
		result := <-completed
		if result.err == nil {
			winners++
			winner = result.mission
		} else if !errors.Is(result.err, missions.ErrMissionExists) {
			t.Errorf("concurrent create = %v, want ErrMissionExists", result.err)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent create accepted %d missions, want one", winners)
	}
	for _, catalog := range catalogs {
		items, err := catalog.List(t.Context())
		if err != nil || !reflect.DeepEqual(items, []missions.Mission{winner}) {
			t.Fatalf("concurrent create changed the winning definition: %#v, error=%v", items, err)
		}
	}
}

func TestConcurrentMissionUpserts(t *testing.T) {
	database := openStore(t, "missions")
	catalog := mongomissions.NewMissionMongoStore(database)
	const writers = 12
	completed := make(chan error, writers)

	for i := range writers {
		go func() {
			mission := missions.Mission{Name: "shared", Objectives: []string{fmt.Sprintf("Revision %d", i)}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
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

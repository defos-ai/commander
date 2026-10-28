package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/defos-ai/commander/framework/domain/missions"
	missionfs "github.com/defos-ai/commander/providers/fs/missions"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestMissionCreationRejectsInvalidCancelledAndDuplicateDefinitions(t *testing.T) {
	root := t.TempDir()
	store, err := missionfs.NewMissionFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	definition := fixtures.Mission()
	invalid := definition.Clone()
	invalid.InputSchema = json.RawMessage(`{"type":"unknown"}`)
	if err := store.Create(t.Context(), invalid); err == nil {
		t.Fatal("created a mission with an invalid schema")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.Create(cancelled, definition); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled creation: %v", err)
	}
	items, err := store.List(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("rejected creation changed the catalog: %+v, %v", items, err)
	}
	if err := store.Create(t.Context(), definition); err != nil {
		t.Fatal(err)
	}
	changed := definition.Clone()
	changed.Objectives = []string{"Replace the original objectives"}
	for _, duplicate := range []missions.Mission{definition, changed} {
		if err := store.Create(t.Context(), duplicate); !errors.Is(err, missions.ErrMissionExists) {
			t.Fatalf("duplicate creation must conflict: %v", err)
		}
	}
	stored, err := store.Get(t.Context(), definition.Name)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, stored, definition)
	stored.Objectives[0] = "changed"
	stored.RequiredActions[0] = "changed"
	stored.ActionDefinitions[0].Name = "changed"
	stored.ActionDefinitions[0].RequiredSecrets[0] = "CHANGED"
	stored.ActionDefinitions[0].InputSchema[0] = '['
	stored.ActionDefinitions[0].OutputSchema[0] = '['
	stored.Rules[missions.AggregateResults].ForActions[0] = "changed"
	stored.InputSchema[0] = '['
	reopened, err := missionfs.NewMissionFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	items, err = reopened.List(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("read snapshots changed saved state: %+v, %v", items, err)
	}
	assertResultJSONEqual(t, items[0], definition)
	changed.ActionDefinitions = nil
	if err := reopened.Upsert(t.Context(), changed); err != nil {
		t.Fatalf("startup seeding can still update an existing mission: %v", err)
	}
	stored, err = store.Get(t.Context(), definition.Name)
	if err != nil {
		t.Fatalf("seeded update was not saved: %+v, %v", stored, err)
	}
	assertResultJSONEqual(t, stored, changed)
}

func TestMissionCreationIsAtomicAcrossStoreInstances(t *testing.T) {
	root := t.TempDir()
	const attempts = 12
	stores := make([]*missionfs.MissionFsStore, attempts)
	for i := range stores {
		var err error
		stores[i], err = missionfs.NewMissionFsStore(root)
		if err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		definition missions.Mission
		err        error
	}
	gate, results := make(chan struct{}), make(chan result, attempts)
	for i, store := range stores {
		go func() {
			definition := fixtures.Mission()
			definition.Objectives = []string{fmt.Sprintf("Candidate %d", i)}
			<-gate
			results <- result{definition, store.Create(t.Context(), definition)}
		}()
	}
	close(gate)
	var winner missions.Mission
	winners := 0
	for range attempts {
		created := <-results
		if created.err == nil {
			winner = created.definition
			winners++
		} else if !errors.Is(created.err, missions.ErrMissionExists) {
			t.Errorf("concurrent creation: %v", created.err)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent creation produced %d winners", winners)
	}
	for _, store := range stores {
		items, err := store.List(t.Context())
		if err != nil || len(items) != 1 {
			t.Fatalf("store did not read the complete winning definition: %+v, %v", items, err)
		}
		assertResultJSONEqual(t, items[0], winner)
	}
}

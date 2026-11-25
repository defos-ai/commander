package providers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestMissionDeletionSerializesWithOtherCatalogWrites(t *testing.T) {
	root := t.TempDir()
	first, second := fixtures.OpenMissionStore(t, root), fixtures.OpenMissionStore(t, root)
	original, other, added := fixtures.Mission("review/../scan"), fixtures.Mission("beta"), fixtures.Mission("gamma")
	for _, mission := range []missions.Mission{original, other} {
		if err := first.Create(t.Context(), mission); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := first.Delete(cancelled, original.Name); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled delete = %v", err)
	}
	saved, err := second.Get(t.Context(), original.Name)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, original)
	start, deletions, creation := make(chan struct{}), make(chan error, 2), make(chan error, 1)
	for _, store := range []missions.MissionStore{first, second} {
		go func() {
			<-start
			deletions <- store.Delete(t.Context(), original.Name)
		}()
	}
	go func() {
		<-start
		creation <- second.Create(t.Context(), added)
	}()

	close(start)
	a, b, created := <-deletions, <-deletions, <-creation

	if !((a == nil && errors.Is(b, missions.ErrUnknownMission)) || (b == nil && errors.Is(a, missions.ErrUnknownMission))) {
		t.Fatalf("concurrent deletes = %v, %v; want one success and one missing mission", a, b)
	}
	if created != nil {
		t.Fatalf("concurrent creation failed: %v", created)
	}
	reopened := fixtures.OpenMissionStore(t, root)
	items, err := reopened.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, items, []missions.Mission{other, added})
	if _, err := reopened.Get(t.Context(), original.Name); !errors.Is(err, missions.ErrUnknownMission) {
		t.Fatalf("deleted mission remained after reopen: %v", err)
	}
	if err := reopened.Update(t.Context(), original); !errors.Is(err, missions.ErrUnknownMission) {
		t.Fatalf("update recreated a deleted mission: %v", err)
	}
	if err := reopened.Create(t.Context(), original); err != nil {
		t.Fatalf("deleted name could not be reused: %v", err)
	}
	updated := original.Clone()
	updated.Objectives = []string{"Review the revised input"}
	if err := reopened.Update(cancelled, updated); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled update = %v", err)
	}
	saved, err = first.Get(t.Context(), original.Name)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, original)
	start, deletion, update := make(chan struct{}), make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		deletion <- first.Delete(t.Context(), original.Name)
	}()
	go func() {
		<-start
		update <- second.Update(t.Context(), updated)
	}()

	close(start)
	deleted, revised := <-deletion, <-update

	if deleted != nil || (revised != nil && !errors.Is(revised, missions.ErrUnknownMission)) {
		t.Fatalf("concurrent deletion and update = %v, %v", deleted, revised)
	}
	if _, err := fixtures.OpenMissionStore(t, root).Get(t.Context(), original.Name); !errors.Is(err, missions.ErrUnknownMission) {
		t.Fatalf("update resurrected a concurrently deleted mission: %v", err)
	}
}

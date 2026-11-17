package providers_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestAgentFsStoreVersionsSurviveReopenAndNewerObservations(t *testing.T) {
	root := t.TempDir()
	catalog := fixtures.OpenAgentStore(t, root)
	observed := time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC)
	original, other := fixtures.Agent(1, observed), fixtures.Agent(2, observed)
	original.Version = "v1.2.3"
	for _, record := range []agents.Agent{original, other} {
		if err := catalog.Upsert(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}

	catalog = fixtures.OpenAgentStore(t, root)
	stored, err := catalog.Get(t.Context(), original.ID)
	if err != nil || !reflect.DeepEqual(stored, original) {
		t.Fatalf("restart changed reported agent version or content: %#v, error = %v", stored, err)
	}
	items, err := catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []agents.Agent{original, other}) {
		t.Fatalf("inventory changed reported or unreported agents: %#v, error = %v", items, err)
	}

	updated := original.Clone()
	updated.Version = "v1.2.4"
	updated.ObservedAt = observed.Add(time.Nanosecond)
	if err := catalog.Upsert(t.Context(), updated); err != nil {
		t.Fatalf("strictly newer observation rejected: %v", err)
	}

	catalog = fixtures.OpenAgentStore(t, root)
	stored, err = catalog.Get(t.Context(), original.ID)
	if err != nil || !reflect.DeepEqual(stored, updated) {
		t.Fatalf("restart lost updated version: %#v, error = %v", stored, err)
	}

	stale := original.Clone()
	stale.LastHeartbeat = observed.Add(time.Hour)
	if err := catalog.Upsert(t.Context(), stale); !errors.Is(err, agents.ErrStaleObservation) {
		t.Fatalf("stale observation replaced updated agent version: %v", err)
	}
	items, err = catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []agents.Agent{updated, other}) {
		t.Fatalf("stale observation changed saved version or another agent: %#v, error = %v", items, err)
	}
}

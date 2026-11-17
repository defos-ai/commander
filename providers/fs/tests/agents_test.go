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

func TestAgentFsStoreStopIntentSurvivesObservationsRestartAndLeaseExpiry(t *testing.T) {
	root := t.TempDir()
	catalog := fixtures.OpenAgentStore(t, root)
	observed := time.Date(2026, 11, 17, 12, 0, 0, 123456789, time.UTC)
	original := fixtures.Agent(1, observed)
	commandID, replacementID := "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"
	requestedAt, replacementAt := observed.Add(time.Nanosecond), observed.Add(2*time.Nanosecond)
	original.StopCommandID, original.StopRequestedAt = replacementID, replacementAt
	if err := catalog.Upsert(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	stored, err := catalog.Get(t.Context(), original.ID)
	if err != nil || stored.StopCommandID != "" || !stored.StopRequestedAt.IsZero() {
		t.Fatalf("observation created controller-owned intent: %#v, error = %v", stored, err)
	}

	accepted, err := catalog.RequestStop(t.Context(), original.ID, commandID, requestedAt)
	if err != nil || accepted.StopCommandID != commandID || !accepted.StopRequestedAt.Equal(requestedAt) || accepted.ObservedAt != observed {
		t.Fatalf("stop request changed observation or lost intent: %#v, error = %v", accepted, err)
	}
	errorsSeen := make(chan error, 12)
	for index := range 6 {
		go func() {
			_, err := catalog.RequestStop(t.Context(), original.ID, replacementID, replacementAt)
			errorsSeen <- err
		}()
		go func() {
			heartbeat := original.Clone()
			heartbeat.ObservedAt = observed.Add(time.Duration(index+1) * time.Nanosecond)
			errorsSeen <- catalog.Upsert(t.Context(), heartbeat)
		}()
	}
	var concurrentError error
	for range 12 {
		if err := <-errorsSeen; err != nil && !errors.Is(err, agents.ErrStaleObservation) {
			concurrentError = err
		}
	}
	if concurrentError != nil {
		t.Fatal(concurrentError)
	}
	catalog = fixtures.OpenAgentStore(t, root)
	stored, err = catalog.Get(t.Context(), original.ID)
	if err != nil || stored.Status != agents.AgentStopRequested || stored.StopCommandID != commandID || !stored.StopRequestedAt.Equal(requestedAt) {
		t.Fatalf("concurrent observations or restart replaced stop intent: %#v, error = %v", stored, err)
	}

	renewed := stored.Clone()
	renewed.LastHeartbeat = stored.LastHeartbeat.Add(time.Second)
	renewed.LeaseExpiresAt = renewed.LastHeartbeat.Add(agents.DefaultHeartbeatTimeout)
	renewed.StopCommandID, renewed.StopRequestedAt = replacementID, replacementAt
	if err := catalog.Upsert(t.Context(), renewed); err != nil {
		t.Fatalf("equal-observation heartbeat could not renew its lease: %v", err)
	}
	cutoff := renewed.LastHeartbeat.Add(time.Hour)
	removed, err := catalog.DeleteExpired(t.Context(), cutoff)
	if err != nil || len(removed) != 0 {
		t.Fatalf("lease expiry removed unconfirmed stop: %#v, error = %v", removed, err)
	}
	catalog = fixtures.OpenAgentStore(t, root)
	stored, err = catalog.Get(t.Context(), original.ID)
	if err != nil || stored.Status != agents.AgentStopRequested || stored.StopCommandID != commandID || !stored.StopRequestedAt.Equal(requestedAt) || !stored.LastHeartbeat.Equal(renewed.LastHeartbeat) || !stored.Snapshot(cutoff).Stale {
		t.Fatalf("pending stop was not retained stale with its renewed lease: %#v, error = %v", stored, err)
	}

	stopped := stored.Clone()
	stopped.Status, stopped.ObservedAt = agents.AgentStopped, cutoff.Add(time.Nanosecond)
	stopped.StopCommandID, stopped.StopRequestedAt = "", time.Time{}
	if err := catalog.Upsert(t.Context(), stopped); err != nil {
		t.Fatal(err)
	}
	accepted, err = catalog.RequestStop(t.Context(), original.ID, replacementID, replacementAt)
	if err != nil || accepted.Status != agents.AgentStopped || accepted.StopCommandID != commandID || !accepted.StopRequestedAt.Equal(requestedAt) {
		t.Fatalf("stopped observation or repeated stop replaced original intent: %#v, error = %v", accepted, err)
	}
	resumed := stopped.Clone()
	resumed.Status, resumed.ObservedAt = agents.AgentReady, stopped.ObservedAt.Add(time.Nanosecond)
	if err := catalog.Upsert(t.Context(), resumed); err != nil {
		t.Fatal(err)
	}
	stored, err = catalog.Get(t.Context(), original.ID)
	if err != nil || stored.Status != agents.AgentStopRequested || stored.StopCommandID != commandID {
		t.Fatalf("newer ready observation bypassed retained stop intent: %#v, error = %v", stored, err)
	}
	stopped.ObservedAt = resumed.ObservedAt.Add(time.Nanosecond)
	if err := catalog.Upsert(t.Context(), stopped); err != nil {
		t.Fatal(err)
	}
	removed, err = catalog.DeleteExpired(t.Context(), stopped.ObservedAt)
	if err != nil || len(removed) != 1 {
		t.Fatalf("confirmed stopped agent did not expire normally: %#v, error = %v", removed, err)
	}
	if _, err := catalog.RequestStop(t.Context(), original.ID, commandID, requestedAt); !errors.Is(err, agents.ErrUnknownAgent) {
		t.Fatalf("stop request recreated expired agent: %v", err)
	}
}

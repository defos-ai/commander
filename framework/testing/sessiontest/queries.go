// Package sessiontest checks the shared session-query contract against a provider.
package sessiontest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

// CheckQueries requires an empty, isolated store. It retains historical resources
// beside current reservations so discovery cannot accidentally become inventory.
func CheckQueries(t *testing.T, store sessions.Store) {
	t.Helper()
	const (
		scopeA    = "00000000-0000-4000-8000-000000000001"
		scopeB    = "00000000-0000-4000-8000-000000000002"
		activeID  = "00000000-0000-4000-8000-000000000003"
		closedID  = "00000000-0000-4000-8000-000000000004"
		lostID    = "00000000-0000-4000-8000-000000000005"
		pendingID = "00000000-0000-4000-8000-000000000006"
		ownerA    = "fixture-agent-00000001"
		ownerB    = "fixture-agent-00000002"
	)
	ctx := t.Context()
	now := time.Now().UTC()
	apply := func(change sessions.Change) sessions.Record {
		t.Helper()
		change.At = now
		record, err := store.Apply(ctx, change)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	seed := func(scopeID, sessionID, owner string) sessions.Record {
		t.Helper()
		creation := sessions.ExecutionRef{OperationID: id.New(), TaskID: sessionID, CommandID: sessionID}
		record := apply(sessions.Change{Kind: sessions.Reserve, ScopeID: scopeID, ID: sessionID, Creation: creation})
		if owner == "" {
			return record
		}
		record = apply(sessions.Change{Kind: sessions.Bind, ScopeID: scopeID, ID: sessionID, Creation: creation, OwnerAgentID: owner})
		observation := sessions.Session{Reference: record.Reference, Kind: "fixture", Label: "Resource", State: "connected", Revision: 1, CreatedAt: now, UpdatedAt: now}
		return apply(sessions.Change{Kind: sessions.Observe, ScopeID: scopeID, ID: sessionID, Observation: &observation})
	}

	// Insert in a different order from the required scope/session ordering.
	lost := seed(scopeB, lostID, ownerA)
	abandon := sessions.Control{Reference: lost.Reference, Method: "abandon", RequestID: id.New()}
	apply(sessions.Change{Kind: sessions.Abandon, ScopeID: scopeB, ID: lostID, Control: &abandon})
	seed(scopeB, pendingID, "")
	closed := seed(scopeA, closedID, ownerA)
	closeRequest := sessions.Control{Reference: closed.Reference, Method: "close", RequestID: id.New(), Creation: &closed.Creation}
	apply(sessions.Change{Kind: sessions.RequestClose, ScopeID: scopeA, ID: closedID, Control: &closeRequest})
	observation := closed.Observation.Clone()
	observation.State, observation.Revision = "closed", 2
	closed = apply(sessions.Change{Kind: sessions.ConfirmClose, ScopeID: scopeA, ID: closedID, Control: &closeRequest, Observation: &observation})
	seed(scopeA, activeID, ownerB)
	before, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		query sessions.Query
		want  []string
	}{
		{"all retained", sessions.Query{}, []string{activeID, closedID, lostID, pendingID}},
		{"scope", sessions.Query{ScopeID: scopeA}, []string{activeID, closedID}},
		{"owner", sessions.Query{OwnerAgentID: ownerA}, []string{closedID, lostID}},
		{"intersected", sessions.Query{ScopeID: scopeA, OwnerAgentID: ownerA}, []string{closedID}},
		{"identity", sessions.Query{ID: activeID}, []string{activeID}},
		{"identity outside scope", sessions.Query{ID: activeID, ScopeID: scopeB}, []string{}},
		{"unknown scope", sessions.Query{ScopeID: id.New()}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records, err := store.Find(t.Context(), tc.query)
			if err != nil || records == nil {
				t.Fatalf("query failed: records=%+v error=%v", records, err)
			}
			var actual []string
			for _, record := range records {
				actual = append(actual, record.ID)
			}
			if !slices.Equal(actual, tc.want) {
				t.Fatalf("query selected %v; want %v", actual, tc.want)
			}
		})
	}

	items, err := store.Find(ctx, sessions.Query{ID: closedID})
	if err != nil || len(items) != 1 || !reflect.DeepEqual(items[0], closed) {
		t.Fatalf("query changed release evidence: %+v, %v", items, err)
	}
	items[0].Observation.Label = "changed by reader"
	items[0].PendingControl.RequestID = id.New()
	items[0].Release.Reason = "changed by reader"
	after, err := store.List(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("query or caller changed saved lifecycle: before=%+v after=%+v error=%v", before, after, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Find(canceled, sessions.Query{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("query ignored cancellation: %v", err)
	}
}

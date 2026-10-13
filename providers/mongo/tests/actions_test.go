package mongo_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/domain/actions"
	mongoactions "github.com/defos-ai/commander/providers/mongo/actions"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestActionMongoStore(t *testing.T) {
	catalog := mongoactions.NewActionMongoStore(openStore(t, "actions"))
	ctx := t.Context()
	original := actions.Action{
		Name: "alpha", Description: "Inspect a page", Status: actions.Unknown,
		StatusReason:        "Readiness is determined by the agent",
		MissingRequirements: []actions.Requirement{{Kind: actions.ConfigurationRequirement, Name: "API_KEY", Secret: true}},
		InputSchema:         json.RawMessage(`{"type":"application-specific","x-example":9007199254740993}`),
		OutputSchema:        json.RawMessage(`{"type":"object","x-example":18446744073709551615}`),
	}
	other := original
	other.Name, other.Status = "beta", ""
	items, err := catalog.List(ctx)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty catalog = %#v, error = %v", items, err)
	}
	if _, err := catalog.Get(ctx, "missing"); !errors.Is(err, actions.ErrUnknownAction) {
		t.Fatalf("missing action: %v", err)
	}

	for _, item := range []actions.Action{other, original} {
		if err := catalog.Upsert(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := catalog.Get(ctx, original.Name)
	if err != nil || !reflect.DeepEqual(stored, original) {
		t.Fatalf("action changed on round trip: %#v, error = %v", stored, err)
	}
	items, err = catalog.List(ctx)
	if err != nil || !reflect.DeepEqual(items, []actions.Action{original, other}) {
		t.Fatalf("catalog content or order changed: %#v, error = %v", items, err)
	}

	updated := original
	updated.Description = "Inspect before release"
	updated.StatusReason, updated.MissingRequirements = "", nil
	if err := catalog.Upsert(ctx, updated); err != nil {
		t.Fatal(err)
	}

	stored, err = catalog.Get(ctx, updated.Name)
	if err != nil || !reflect.DeepEqual(stored, updated) {
		t.Fatalf("upsert retained old fields: %#v, error = %v", stored, err)
	}
	items, err = catalog.List(ctx)
	if err != nil || !reflect.DeepEqual(items, []actions.Action{updated, other}) {
		t.Fatalf("upsert changed another action or created a duplicate: %#v, error = %v", items, err)
	}
}

func TestActionMongoStoreRejectsInvalidAndCancelledWrites(t *testing.T) {
	catalog := mongoactions.NewActionMongoStore(openStore(t, "actions"))
	original := actions.Action{Name: "inspect", Description: "Original", Status: actions.Unknown, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	if err := catalog.Upsert(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*actions.Action)
	}{
		{name: "ready belongs to inventory", change: func(a *actions.Action) { a.Status = actions.Ready }},
		{name: "unavailable belongs to inventory", change: func(a *actions.Action) { a.Status = actions.Unavailable }},
		{name: "unknown status value", change: func(a *actions.Action) { a.Status = "invalid" }},
		{name: "invalid input schema", change: func(a *actions.Action) { a.InputSchema = json.RawMessage(`[]`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := original
			invalid.Description = "Rejected change"
			test.change(&invalid)

			err := catalog.Upsert(t.Context(), invalid)

			if err == nil {
				t.Fatal("invalid action was accepted")
			}
			stored, err := catalog.Get(t.Context(), original.Name)
			if err != nil || !reflect.DeepEqual(stored, original) {
				t.Fatalf("rejected write changed the action: %#v, error = %v", stored, err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	updated := original
	updated.Description = "Cancelled change"

	if err := catalog.Upsert(cancelled, updated); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upsert: %v", err)
	}
	if _, err := catalog.Get(cancelled, original.Name); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled get: %v", err)
	}
	if _, err := catalog.List(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list: %v", err)
	}
	items, err := catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []actions.Action{original}) {
		t.Fatalf("cancelled write changed the catalog: %#v, error = %v", items, err)
	}
}

func TestActionMongoStoreRejectsCorruptRecords(t *testing.T) {
	database := openStore(t, "actions")
	catalog := mongoactions.NewActionMongoStore(database)
	original := actions.Action{Name: "inspect", Status: actions.Unknown, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	if err := catalog.Upsert(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	result, err := database.Update(t.Context(), "actions", bson.M{"_id": original.Name}, bson.M{"$set": bson.M{"status": actions.Ready}})
	if err != nil || result.MatchedCount != 1 {
		t.Fatalf("could not create corrupt catalog fixture: %+v, error = %v", result, err)
	}

	_, getErr := catalog.Get(t.Context(), original.Name)
	_, listErr := catalog.List(t.Context())

	if getErr == nil || listErr == nil {
		t.Fatalf("invalid persisted readiness escaped catalog validation: get=%v, list=%v", getErr, listErr)
	}
}

func TestConcurrentActionUpserts(t *testing.T) {
	catalog := mongoactions.NewActionMongoStore(openStore(t, "actions"))
	const writers = 12
	completed := make(chan error, writers)
	start := make(chan struct{})
	revisions := make(map[string]actions.Action, writers)
	distinct := make(map[string]actions.Action, writers)
	for i := range writers {
		item := actions.Action{
			Name: "shared", Description: fmt.Sprintf("Revision %d", i), Status: actions.Unknown,
			InputSchema:  json.RawMessage(fmt.Sprintf(`{"revision":%d}`, i)),
			OutputSchema: json.RawMessage(`{}`),
		}
		revisions[item.Description] = item
		separate := item
		separate.Name = fmt.Sprintf("action-%02d", i)
		distinct[separate.Name] = separate
		go func() {
			<-start
			if err := catalog.Upsert(t.Context(), item); err != nil {
				completed <- err
				return
			}
			completed <- catalog.Upsert(t.Context(), separate)
		}()
	}

	close(start)
	for range writers {
		if err := <-completed; err != nil {
			t.Error(err)
		}
	}

	items, err := catalog.List(t.Context())
	if err != nil || len(items) != writers+1 {
		t.Fatalf("concurrent writes lost or duplicated actions: count=%d, error=%v", len(items), err)
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		want, ok := distinct[item.Name]
		if item.Name == "shared" {
			want, ok = revisions[item.Description]
		}
		if !ok || seen[item.Name] || !reflect.DeepEqual(item, want) {
			t.Fatalf("concurrent upserts produced a duplicate or mixed record: %#v", item)
		}
		seen[item.Name] = true
	}
}

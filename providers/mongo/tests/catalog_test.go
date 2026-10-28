package mongo_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	agentmongo "github.com/defos-ai/commander/providers/mongo/agents"
	"github.com/defos-ai/commander/providers/mongo/tests/fixtures"
)

func TestCatalogMongoStorePersistsIndependentReleaseEntries(t *testing.T) {
	database := fixtures.OpenStore(t, "agent_catalog")
	catalog := agentmongo.NewCatalogMongoStore(database)
	first := fixtures.CatalogEntry("alpha", "github.com/defos-ai/alpha-agent@v0.3.0")
	first.Trusted = true
	first.TrustUpdatedBy = "catalog-operator"
	first.TrustUpdatedAt = first.UpdatedAt
	second := fixtures.CatalogEntry("alpha", "github.com/defos-ai/alpha-agent@v0.3.1")
	stable := fixtures.CatalogEntry("alpha", "github.com/defos-ai/alpha-agent@stable")
	for _, entry := range []agents.CatalogEntry{second, first, stable} {
		if err := catalog.Create(t.Context(), entry); err != nil {
			t.Fatal(err)
		}
	}

	reopened := agentmongo.NewCatalogMongoStore(database)
	items, err := reopened.List(t.Context())

	want := []agents.CatalogEntry{stable, first, second}
	if err != nil || !reflect.DeepEqual(items, want) {
		t.Fatalf("persisted catalog changed release identities, ordering or timestamp precision: %v", err)
	}
	items[0].Trusted = false
	got, err := reopened.Get(t.Context(), stable.Package)
	if err != nil || !reflect.DeepEqual(got, stable) {
		t.Fatalf("returned catalog snapshot changed the saved entry: %v", err)
	}
	if err := catalog.Create(t.Context(), first); !errors.Is(err, agents.ErrCatalogEntryExists) {
		t.Fatalf("duplicate package insertion returned %v", err)
	}
	if _, err := reopened.Get(t.Context(), "github.com/defos-ai/missing-agent@stable"); !errors.Is(err, agents.ErrUnknownCatalogEntry) {
		t.Fatalf("missing package lookup returned %v", err)
	}
}

func TestCatalogMongoStorePreservesRevocationAndRejectsStaleUpdates(t *testing.T) {
	database := fixtures.OpenStore(t, "agent_catalog")
	catalog := agentmongo.NewCatalogMongoStore(database)
	initial := fixtures.CatalogEntry("worker", "github.com/defos-ai/worker-agent@v0.3.0")
	initial.Trusted = true
	initial.TrustUpdatedBy = "catalog-operator"
	initial.TrustUpdatedAt = initial.UpdatedAt
	if err := catalog.Create(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	revoked := fixtures.CatalogReplacement(initial, false)

	if err := catalog.Replace(t.Context(), revoked, initial.Version); err != nil {
		t.Fatal(err)
	}

	if err := catalog.Create(t.Context(), initial); !errors.Is(err, agents.ErrCatalogEntryExists) {
		t.Fatalf("reseed could replace a saved revocation: %v", err)
	}
	if err := catalog.Replace(t.Context(), revoked, initial.Version); !errors.Is(err, agents.ErrCatalogConflict) {
		t.Fatalf("stale trust update returned %v", err)
	}
	for _, change := range []string{"creation time", "update time", "version", "overflow"} {
		t.Run(change, func(t *testing.T) {
			next := fixtures.CatalogReplacement(revoked, true)
			expected := revoked.Version
			switch change {
			case "creation time":
				next.CreatedAt = next.CreatedAt.Add(-time.Nanosecond)
			case "update time":
				next.UpdatedAt = initial.UpdatedAt
				next.TrustUpdatedAt = next.UpdatedAt
			case "version":
				next.Version++
			case "overflow":
				expected = math.MaxUint64
			}

			if err := catalog.Replace(t.Context(), next, expected); !errors.Is(err, agents.ErrCatalogConflict) {
				t.Fatalf("invalid replacement returned %v", err)
			}
		})
	}
	got, err := agentmongo.NewCatalogMongoStore(database).Get(t.Context(), revoked.Package)
	if err != nil || !reflect.DeepEqual(got, revoked) {
		t.Fatalf("rejected trust changes modified saved revocation: %v", err)
	}
	missing := fixtures.CatalogReplacement(fixtures.CatalogEntry("missing", "github.com/defos-ai/missing-agent@v0.3.0"), true)
	if err := catalog.Replace(t.Context(), missing, 1); !errors.Is(err, agents.ErrUnknownCatalogEntry) {
		t.Fatalf("missing replacement returned %v", err)
	}
}

func TestCatalogMongoStoreSerializesConcurrentCreatesAndTrustChanges(t *testing.T) {
	catalog := agentmongo.NewCatalogMongoStore(fixtures.OpenStore(t, "agent_catalog"))
	initial := fixtures.CatalogEntry("worker", "github.com/defos-ai/worker-agent@v0.3.0")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const workers = 8
	results := make(chan error, workers)
	for range workers {
		go func() { results <- catalog.Create(ctx, initial) }()
	}
	writes := 0
	var unexpected []error
	for range workers {
		err := <-results
		if err == nil {
			writes++
		} else if !errors.Is(err, agents.ErrCatalogEntryExists) {
			unexpected = append(unexpected, err)
		}
	}
	if len(unexpected) != 0 {
		t.Fatalf("concurrent insertion returned %v", unexpected)
	}
	if writes != 1 {
		t.Fatalf("concurrent insertion committed %d records", writes)
	}
	grant := fixtures.CatalogReplacement(initial, true)
	deny := fixtures.CatalogReplacement(initial, false)
	deny.Description = "A concurrent explicit denial."
	for _, next := range []agents.CatalogEntry{grant, deny} {
		go func() { results <- catalog.Replace(ctx, next, initial.Version) }()
	}
	writes = 0
	for range 2 {
		err := <-results
		if err == nil {
			writes++
		} else if !errors.Is(err, agents.ErrCatalogConflict) {
			unexpected = append(unexpected, err)
		}
	}
	if len(unexpected) != 0 {
		t.Fatalf("concurrent trust update returned %v", unexpected)
	}
	if writes != 1 {
		t.Fatalf("concurrent trust updates committed %d winners", writes)
	}
	got, err := catalog.Get(ctx, initial.Package)
	if err != nil || !reflect.DeepEqual(got, grant) && !reflect.DeepEqual(got, deny) {
		t.Fatalf("concurrent trust updates did not retain one complete version: %v", err)
	}
}

func TestCatalogMongoStoreRejectsCorruptedIndexedState(t *testing.T) {
	for _, change := range []string{"identity", "version", "extra field", "payload field"} {
		t.Run(change, func(t *testing.T) {
			database := fixtures.OpenStore(t, "agent_catalog")
			catalog := agentmongo.NewCatalogMongoStore(database)
			entry := fixtures.CatalogEntry("worker", "github.com/defos-ai/worker-agent@v0.3.0")
			if err := catalog.Create(t.Context(), entry); err != nil {
				t.Fatal(err)
			}
			fixtures.CorruptCatalogDocument(t, database, entry, change)

			_, getErr := catalog.Get(t.Context(), entry.Package)
			_, listErr := catalog.List(t.Context())
			replaceErr := catalog.Replace(t.Context(), fixtures.CatalogReplacement(entry, true), entry.Version)

			if getErr == nil || listErr == nil || replaceErr == nil {
				t.Fatal("corrupt catalog state was accepted or replaced")
			}
		})
	}
}

func TestCatalogMongoStoreChecksCancellationBeforeIO(t *testing.T) {
	catalog := agentmongo.NewCatalogMongoStore(nil)
	entry := fixtures.CatalogEntry("worker", "github.com/defos-ai/worker-agent@stable")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, call := range []func() error{
		func() error { return catalog.Create(ctx, entry) },
		func() error { _, err := catalog.Get(ctx, entry.Package); return err },
		func() error { _, err := catalog.List(ctx); return err },
		func() error { return catalog.Replace(ctx, fixtures.CatalogReplacement(entry, true), entry.Version) },
	} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled catalog operation returned %v", err)
		}
	}
}

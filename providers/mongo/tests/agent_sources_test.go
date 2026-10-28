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

func TestAgentSourceMongoStorePersistsIndependentReleaseEntries(t *testing.T) {
	database := fixtures.OpenStore(t, "agent_sources")
	sources := agentmongo.NewAgentSourceMongoStore(database)
	first := fixtures.AgentSource("alpha", "github.com/defos-ai/alpha-agent@v0.3.0")
	first.Trusted = true
	first.TrustUpdatedBy = "sources-operator"
	first.TrustUpdatedAt = first.UpdatedAt
	second := fixtures.AgentSource("alpha", "github.com/defos-ai/alpha-agent@v0.3.1")
	stable := fixtures.AgentSource("alpha", "github.com/defos-ai/alpha-agent@stable")
	for _, entry := range []agents.AgentSource{second, first, stable} {
		if err := sources.Create(t.Context(), entry); err != nil {
			t.Fatal(err)
		}
	}

	reopened := agentmongo.NewAgentSourceMongoStore(database)
	items, err := reopened.List(t.Context())

	want := []agents.AgentSource{stable, first, second}
	if err != nil || !reflect.DeepEqual(items, want) {
		t.Fatalf("persisted sources changed release identities, ordering or timestamp precision: %v", err)
	}
	items[0].Trusted = false
	got, err := reopened.Get(t.Context(), stable.Source)
	if err != nil || !reflect.DeepEqual(got, stable) {
		t.Fatalf("returned sources snapshot changed the saved entry: %v", err)
	}
	if err := sources.Create(t.Context(), first); !errors.Is(err, agents.ErrAgentSourceExists) {
		t.Fatalf("duplicate package insertion returned %v", err)
	}
	if _, err := reopened.Get(t.Context(), "github.com/defos-ai/missing-agent@stable"); !errors.Is(err, agents.ErrUnknownAgentSource) {
		t.Fatalf("missing package lookup returned %v", err)
	}
}

func TestAgentSourceMongoStorePreservesRevocationAndRejectsStaleUpdates(t *testing.T) {
	database := fixtures.OpenStore(t, "agent_sources")
	sources := agentmongo.NewAgentSourceMongoStore(database)
	initial := fixtures.AgentSource("worker", "github.com/defos-ai/worker-agent@v0.3.0")
	initial.Trusted = true
	initial.TrustUpdatedBy = "sources-operator"
	initial.TrustUpdatedAt = initial.UpdatedAt
	if err := sources.Create(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	revoked := fixtures.AgentSourceReplacement(initial, false)

	if err := sources.Replace(t.Context(), revoked, initial.Version); err != nil {
		t.Fatal(err)
	}

	if err := sources.Create(t.Context(), initial); !errors.Is(err, agents.ErrAgentSourceExists) {
		t.Fatalf("reseed could replace a saved revocation: %v", err)
	}
	if err := sources.Replace(t.Context(), revoked, initial.Version); !errors.Is(err, agents.ErrAgentSourceConflict) {
		t.Fatalf("stale trust update returned %v", err)
	}
	for _, change := range []string{"creation time", "update time", "version", "overflow"} {
		t.Run(change, func(t *testing.T) {
			next := fixtures.AgentSourceReplacement(revoked, true)
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

			if err := sources.Replace(t.Context(), next, expected); !errors.Is(err, agents.ErrAgentSourceConflict) {
				t.Fatalf("invalid replacement returned %v", err)
			}
		})
	}
	got, err := agentmongo.NewAgentSourceMongoStore(database).Get(t.Context(), revoked.Source)
	if err != nil || !reflect.DeepEqual(got, revoked) {
		t.Fatalf("rejected trust changes modified saved revocation: %v", err)
	}
	missing := fixtures.AgentSourceReplacement(fixtures.AgentSource("missing", "github.com/defos-ai/missing-agent@v0.3.0"), true)
	if err := sources.Replace(t.Context(), missing, 1); !errors.Is(err, agents.ErrUnknownAgentSource) {
		t.Fatalf("missing replacement returned %v", err)
	}
}

func TestAgentSourceMongoStoreSerializesConcurrentCreatesAndTrustChanges(t *testing.T) {
	sources := agentmongo.NewAgentSourceMongoStore(fixtures.OpenStore(t, "agent_sources"))
	initial := fixtures.AgentSource("worker", "github.com/defos-ai/worker-agent@v0.3.0")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const workers = 8
	results := make(chan error, workers)
	for range workers {
		go func() { results <- sources.Create(ctx, initial) }()
	}
	writes := 0
	var unexpected []error
	for range workers {
		err := <-results
		if err == nil {
			writes++
		} else if !errors.Is(err, agents.ErrAgentSourceExists) {
			unexpected = append(unexpected, err)
		}
	}
	if len(unexpected) != 0 {
		t.Fatalf("concurrent insertion returned %v", unexpected)
	}
	if writes != 1 {
		t.Fatalf("concurrent insertion committed %d records", writes)
	}
	grant := fixtures.AgentSourceReplacement(initial, true)
	deny := fixtures.AgentSourceReplacement(initial, false)
	deny.Description = "A concurrent explicit denial."
	for _, next := range []agents.AgentSource{grant, deny} {
		go func() { results <- sources.Replace(ctx, next, initial.Version) }()
	}
	writes = 0
	for range 2 {
		err := <-results
		if err == nil {
			writes++
		} else if !errors.Is(err, agents.ErrAgentSourceConflict) {
			unexpected = append(unexpected, err)
		}
	}
	if len(unexpected) != 0 {
		t.Fatalf("concurrent trust update returned %v", unexpected)
	}
	if writes != 1 {
		t.Fatalf("concurrent trust updates committed %d winners", writes)
	}
	got, err := sources.Get(ctx, initial.Source)
	if err != nil || !reflect.DeepEqual(got, grant) && !reflect.DeepEqual(got, deny) {
		t.Fatalf("concurrent trust updates did not retain one complete version: %v", err)
	}
}

func TestAgentSourceMongoStoreRejectsCorruptedIndexedState(t *testing.T) {
	for _, change := range []string{"identity", "version", "extra field", "payload field"} {
		t.Run(change, func(t *testing.T) {
			database := fixtures.OpenStore(t, "agent_sources")
			sources := agentmongo.NewAgentSourceMongoStore(database)
			entry := fixtures.AgentSource("worker", "github.com/defos-ai/worker-agent@v0.3.0")
			if err := sources.Create(t.Context(), entry); err != nil {
				t.Fatal(err)
			}
			fixtures.CorruptAgentSourceDocument(t, database, entry, change)

			_, getErr := sources.Get(t.Context(), entry.Source)
			_, listErr := sources.List(t.Context())
			replaceErr := sources.Replace(t.Context(), fixtures.AgentSourceReplacement(entry, true), entry.Version)

			if getErr == nil || listErr == nil || replaceErr == nil {
				t.Fatal("corrupt sources state was accepted or replaced")
			}
		})
	}
}

func TestAgentSourceMongoStoreChecksCancellationBeforeIO(t *testing.T) {
	sources := agentmongo.NewAgentSourceMongoStore(nil)
	entry := fixtures.AgentSource("worker", "github.com/defos-ai/worker-agent@stable")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, call := range []func() error{
		func() error { return sources.Create(ctx, entry) },
		func() error { _, err := sources.Get(ctx, entry.Source); return err },
		func() error { _, err := sources.List(ctx); return err },
		func() error { return sources.Replace(ctx, fixtures.AgentSourceReplacement(entry, true), entry.Version) },
	} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled sources operation returned %v", err)
		}
	}
}

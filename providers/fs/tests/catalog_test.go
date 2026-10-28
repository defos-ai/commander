package providers_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	agentfs "github.com/defos-ai/commander/providers/fs/agents"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestCatalogFsStorePersistsRevocationAndRejectsStaleChanges(t *testing.T) {
	root := t.TempDir()
	store := fixtures.OpenCatalogStore(t, root)
	initial := fixtures.CatalogEntry(1, true)
	other := fixtures.CatalogEntry(2, false)
	if items, err := store.List(t.Context()); err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty catalog = %#v, %v", items, err)
	}
	for _, entry := range []agents.CatalogEntry{other, initial} {
		if err := store.Create(t.Context(), entry); err != nil {
			t.Fatal(err)
		}
	}
	revoked := fixtures.CatalogReplacement(initial, false)

	if err := store.Replace(t.Context(), revoked, initial.Version); err != nil {
		t.Fatal(err)
	}

	reopened := fixtures.OpenCatalogStore(t, root)
	stored, err := reopened.Get(t.Context(), initial.Package)
	if err != nil || !reflect.DeepEqual(stored, revoked) {
		t.Fatalf("reopened revocation = %#v, %v", stored, err)
	}
	if err := reopened.Create(t.Context(), initial); !errors.Is(err, agents.ErrCatalogEntryExists) {
		t.Fatalf("duplicate creation overwrote revoked trust: %v", err)
	}
	stale := fixtures.CatalogReplacement(initial, true)
	if err := reopened.Replace(t.Context(), stale, initial.Version); !errors.Is(err, agents.ErrCatalogConflict) {
		t.Fatalf("stale trust grant was accepted: %v", err)
	}
	items, err := reopened.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []agents.CatalogEntry{revoked, other}) {
		t.Fatalf("sorted catalog changed after conflicting writes: %#v, %v", items, err)
	}
	items[0].Trusted = true
	stored, err = reopened.Get(t.Context(), initial.Package)
	if err != nil || stored.Trusted {
		t.Fatal("caller changed persisted catalog through a returned snapshot")
	}
}

func TestCatalogFsStoreRejectsInvalidReplacementTransitions(t *testing.T) {
	root := t.TempDir()
	store := fixtures.OpenCatalogStore(t, root)
	initial := fixtures.CatalogEntry(1, true)
	if err := store.Create(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	current := fixtures.CatalogReplacement(initial, false)
	current.UpdatedAt = current.UpdatedAt.Add(time.Second)
	if err := store.Replace(t.Context(), current, initial.Version); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name     string
		expected uint64
		change   func(*agents.CatalogEntry)
	}{
		{"zero expected version", 0, func(*agents.CatalogEntry) {}},
		{"overflow expected version", math.MaxUint64, func(*agents.CatalogEntry) {}},
		{"stale expected version", 1, func(*agents.CatalogEntry) {}},
		{"skipped version", 2, func(entry *agents.CatalogEntry) { entry.Version++ }},
		{"creation time changed", 2, func(entry *agents.CatalogEntry) { entry.CreatedAt = entry.CreatedAt.Add(-time.Nanosecond) }},
		{"update time regressed", 2, func(entry *agents.CatalogEntry) {
			entry.UpdatedAt = current.UpdatedAt.Add(-time.Nanosecond)
			entry.TrustUpdatedAt = entry.UpdatedAt
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			next := fixtures.CatalogReplacement(current, true)
			scenario.change(&next)

			err := store.Replace(t.Context(), next, scenario.expected)

			if !errors.Is(err, agents.ErrCatalogConflict) {
				t.Fatalf("invalid replacement returned %v", err)
			}
			stored, err := store.Get(t.Context(), initial.Package)
			if err != nil || !reflect.DeepEqual(stored, current) {
				t.Fatal("failed replacement changed saved state")
			}
		})
	}
	missing := fixtures.CatalogReplacement(fixtures.CatalogEntry(2, false), true)
	if err := store.Replace(t.Context(), missing, 1); !errors.Is(err, agents.ErrUnknownCatalogEntry) {
		t.Fatalf("missing replacement returned %v", err)
	}
	if _, err := store.Get(t.Context(), missing.Package); !errors.Is(err, agents.ErrUnknownCatalogEntry) {
		t.Fatalf("missing lookup returned %v", err)
	}
	invalid := fixtures.CatalogEntry(3, false)
	invalid.Version = 2
	if err := store.Create(t.Context(), invalid); err == nil {
		t.Fatal("creation accepted a preexisting business version")
	}
}

func TestCatalogFsStoreSerializesConcurrentCreatesAndReplacements(t *testing.T) {
	root := t.TempDir()
	stores := []*agentfs.CatalogFsStore{fixtures.OpenCatalogStore(t, root), fixtures.OpenCatalogStore(t, root)}
	initial := fixtures.CatalogEntry(1, true)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	created := make(chan error, 8)
	for i := range 8 {
		go func() { created <- stores[i%2].Create(ctx, initial) }()
	}

	winners := 0
	var failure error
	for range 8 {
		var err error
		select {
		case err = <-created:
		case <-ctx.Done():
			t.Fatal("concurrent catalog creations did not finish")
		}
		if err == nil {
			winners++
		} else if !errors.Is(err, agents.ErrCatalogEntryExists) {
			failure = err
		}
	}
	if failure != nil {
		t.Fatal(failure)
	}
	if winners != 1 {
		t.Fatalf("concurrent creations had %d winners", winners)
	}
	type replacement struct {
		entry agents.CatalogEntry
		err   error
	}
	replaced := make(chan replacement, 2)
	for i := range 2 {
		next := fixtures.CatalogReplacement(initial, i == 0)
		next.Name = []string{"first-candidate", "second-candidate"}[i]
		go func() { replaced <- replacement{next, stores[i].Replace(ctx, next, initial.Version)} }()
	}

	winners = 0
	var wanted agents.CatalogEntry
	for range 2 {
		var result replacement
		select {
		case result = <-replaced:
		case <-ctx.Done():
			t.Fatal("concurrent catalog replacements did not finish")
		}
		if result.err == nil {
			winners++
			wanted = result.entry
		} else if !errors.Is(result.err, agents.ErrCatalogConflict) {
			failure = result.err
		}
	}
	if failure != nil {
		t.Fatal(failure)
	}
	stored, err := stores[0].Get(t.Context(), initial.Package)
	if err != nil || winners != 1 || !reflect.DeepEqual(stored, wanted) {
		t.Fatalf("concurrent replacement lost its winner: %#v, %v, winners=%d", stored, err, winners)
	}
}

func TestCatalogFsStoreRejectsCorruptStateWithoutReplacingIt(t *testing.T) {
	for name, data := range fixtures.InvalidCatalogDocuments(t) {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			store := fixtures.OpenCatalogStore(t, root)
			fixtures.WriteCatalogBytes(t, root, data)
			entry := fixtures.CatalogEntry(1, false)

			_, openErr := agentfs.NewCatalogFsStore(root)
			_, readErr := store.Get(t.Context(), entry.Package)
			_, listErr := store.List(t.Context())
			writeErr := store.Create(t.Context(), entry)

			if openErr == nil || readErr == nil || listErr == nil || writeErr == nil {
				t.Fatal("corrupt state was accepted or repaired")
			}
			if !bytes.Equal(fixtures.ReadCatalogBytes(t, root), data) {
				t.Fatal("rejected state was overwritten")
			}
		})
	}
}

func TestCatalogFsStoreHonorsCancellationBeforeReadingOrWriting(t *testing.T) {
	root := t.TempDir()
	store := fixtures.OpenCatalogStore(t, root)
	data := []byte("invalid state that canceled operations must not inspect")
	fixtures.WriteCatalogBytes(t, root, data)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	entry := fixtures.CatalogEntry(1, false)

	_, readErr := store.Get(ctx, entry.Package)
	_, listErr := store.List(ctx)
	createErr := store.Create(ctx, entry)
	replaceErr := store.Replace(ctx, fixtures.CatalogReplacement(entry, true), 1)

	for _, err := range []error{readErr, listErr, createErr, replaceErr} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled operation returned %v", err)
		}
	}
	if !bytes.Equal(fixtures.ReadCatalogBytes(t, root), data) {
		t.Fatal("canceled operation changed stored state")
	}
}

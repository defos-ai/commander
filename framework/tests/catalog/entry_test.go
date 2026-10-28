package catalog_test

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/catalog"
)

func TestCatalogEntryRequiresCanonicalIdentityAndValidMetadata(t *testing.T) {
	entry := fixtures.Entry()
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name  string
		apply func(*agents.CatalogEntry)
	}{
		{"unversioned package", func(e *agents.CatalogEntry) { e.Package = "github.com/example/alpha-agent" }},
		{"package URL", func(e *agents.CatalogEntry) { e.Package = "https://github.com/example/alpha-agent@stable" }},
		{"invalid alias", func(e *agents.CatalogEntry) { e.Name = "InvalidAlias" }},
		{"padded description", func(e *agents.CatalogEntry) { e.Description = " padded " }},
		{"unknown visibility", func(e *agents.CatalogEntry) { e.Visibility = packages.Visibility("internal") }},
		{"zero version", func(e *agents.CatalogEntry) { e.Version = 0 }},
		{"missing creation time", func(e *agents.CatalogEntry) { e.CreatedAt = time.Time{} }},
		{"backward update", func(e *agents.CatalogEntry) { e.UpdatedAt = e.CreatedAt.Add(-time.Nanosecond) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			invalid := entry
			change.apply(&invalid)

			err := invalid.Validate()

			if err == nil {
				t.Fatal("invalid catalog identity or metadata was accepted")
			}
		})
	}

	pinned := entry
	pinned.Package = "github.com/example/alpha-agent@v1.2.3"
	if err := pinned.Validate(); err != nil || pinned.Package == entry.Package {
		t.Fatal("exact releases and stable selectors must remain distinct valid keys")
	}
}

func TestCatalogTrustRequiresExactReleaseAndCompleteAudit(t *testing.T) {
	entry := fixtures.TrustedEntry()
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name  string
		apply func(*agents.CatalogEntry)
	}{
		{"moving selector", func(e *agents.CatalogEntry) { e.Package = "github.com/example/alpha-agent@stable" }},
		{"missing actor", func(e *agents.CatalogEntry) { e.TrustUpdatedBy = "" }},
		{"blank actor", func(e *agents.CatalogEntry) { e.TrustUpdatedBy = " \t" }},
		{"control in actor", func(e *agents.CatalogEntry) { e.TrustUpdatedBy = "operator\n" }},
		{"missing audit time", func(e *agents.CatalogEntry) { e.TrustUpdatedAt = time.Time{} }},
		{"audit before creation", func(e *agents.CatalogEntry) { e.TrustUpdatedAt = e.CreatedAt.Add(-time.Nanosecond) }},
		{"audit after update", func(e *agents.CatalogEntry) { e.TrustUpdatedAt = e.UpdatedAt.Add(time.Nanosecond) }},
		{"audit outside timestamp range", func(e *agents.CatalogEntry) {
			e.TrustUpdatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			invalid := entry
			change.apply(&invalid)

			err := invalid.Validate()

			if err == nil {
				t.Fatal("invalid trust decision was accepted")
			}
		})
	}
}

func TestCatalogRetainsRevocationAuditAndAllowsNoDecision(t *testing.T) {
	unreviewed := fixtures.Entry()
	revoked := fixtures.TrustedEntry()
	revoked.Trusted = false
	revoked.UpdatedAt = revoked.UpdatedAt.Add(time.Second)
	revoked.TrustUpdatedAt = revoked.UpdatedAt
	revoked.TrustUpdatedBy = "revoking-operator"
	metadataEdited := revoked
	metadataEdited.Description = "Updated package description."
	metadataEdited.UpdatedAt = metadataEdited.UpdatedAt.Add(time.Second)
	metadataEdited.TrustUpdatedBy = " revoking-operator "

	for _, entry := range []agents.CatalogEntry{unreviewed, revoked, metadataEdited} {
		if err := entry.Validate(); err != nil {
			t.Fatalf("valid unreviewed or revoked catalog entry was rejected: %v", err)
		}
	}
	for _, change := range []func(*agents.CatalogEntry){
		func(e *agents.CatalogEntry) { e.TrustUpdatedBy = "operator" },
		func(e *agents.CatalogEntry) { e.TrustUpdatedAt = e.UpdatedAt },
	} {
		partial := unreviewed
		change(&partial)

		if err := partial.Validate(); err == nil {
			t.Fatal("incomplete revocation audit was accepted")
		}
	}
}

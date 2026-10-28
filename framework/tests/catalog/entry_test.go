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
	pinned.Trusted = true
	if err := pinned.Validate(); err != nil || pinned.Package == entry.Package {
		t.Fatal("exact releases and stable selectors must remain distinct valid keys")
	}
}

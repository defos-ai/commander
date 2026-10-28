package agentsources_test

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agentsources"
)

func TestAgentSourceRequiresCanonicalIdentityAndValidMetadata(t *testing.T) {
	entry := fixtures.Source()
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name  string
		apply func(*agents.AgentSource)
	}{
		{"unversioned package", func(e *agents.AgentSource) { e.Source = "github.com/example/alpha-agent" }},
		{"package URL", func(e *agents.AgentSource) { e.Source = "https://github.com/example/alpha-agent@stable" }},
		{"invalid alias", func(e *agents.AgentSource) { e.Name = "InvalidAlias" }},
		{"padded description", func(e *agents.AgentSource) { e.Description = " padded " }},
		{"unknown visibility", func(e *agents.AgentSource) { e.Visibility = packages.Visibility("internal") }},
		{"zero version", func(e *agents.AgentSource) { e.Version = 0 }},
		{"missing creation time", func(e *agents.AgentSource) { e.CreatedAt = time.Time{} }},
		{"backward update", func(e *agents.AgentSource) { e.UpdatedAt = e.CreatedAt.Add(-time.Nanosecond) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			invalid := entry
			change.apply(&invalid)

			err := invalid.Validate()

			if err == nil {
				t.Fatal("invalid agent source identity or metadata was accepted")
			}
		})
	}

	pinned := entry
	pinned.Source = "github.com/example/alpha-agent@v1.2.3"
	if err := pinned.Validate(); err != nil || pinned.Source == entry.Source {
		t.Fatal("exact releases and stable selectors must remain distinct valid keys")
	}
}

func TestAgentSourceTrustRequiresExactReleaseAndCompleteAudit(t *testing.T) {
	entry := fixtures.TrustedSource()
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name  string
		apply func(*agents.AgentSource)
	}{
		{"moving selector", func(e *agents.AgentSource) { e.Source = "github.com/example/alpha-agent@stable" }},
		{"missing actor", func(e *agents.AgentSource) { e.TrustUpdatedBy = "" }},
		{"blank actor", func(e *agents.AgentSource) { e.TrustUpdatedBy = " \t" }},
		{"control in actor", func(e *agents.AgentSource) { e.TrustUpdatedBy = "operator\n" }},
		{"missing audit time", func(e *agents.AgentSource) { e.TrustUpdatedAt = time.Time{} }},
		{"audit before creation", func(e *agents.AgentSource) { e.TrustUpdatedAt = e.CreatedAt.Add(-time.Nanosecond) }},
		{"audit after update", func(e *agents.AgentSource) { e.TrustUpdatedAt = e.UpdatedAt.Add(time.Nanosecond) }},
		{"audit outside timestamp range", func(e *agents.AgentSource) {
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

func TestAgentSourceRetainsRevocationAuditAndAllowsNoDecision(t *testing.T) {
	unreviewed := fixtures.Source()
	revoked := fixtures.TrustedSource()
	revoked.Trusted = false
	revoked.UpdatedAt = revoked.UpdatedAt.Add(time.Second)
	revoked.TrustUpdatedAt = revoked.UpdatedAt
	revoked.TrustUpdatedBy = "revoking-operator"
	metadataEdited := revoked
	metadataEdited.Description = "Updated package description."
	metadataEdited.UpdatedAt = metadataEdited.UpdatedAt.Add(time.Second)
	metadataEdited.TrustUpdatedBy = " revoking-operator "

	for _, entry := range []agents.AgentSource{unreviewed, revoked, metadataEdited} {
		if err := entry.Validate(); err != nil {
			t.Fatalf("valid unreviewed or revoked agent source was rejected: %v", err)
		}
	}
	for _, change := range []func(*agents.AgentSource){
		func(e *agents.AgentSource) { e.TrustUpdatedBy = "operator" },
		func(e *agents.AgentSource) { e.TrustUpdatedAt = e.UpdatedAt },
	} {
		partial := unreviewed
		change(&partial)

		if err := partial.Validate(); err == nil {
			t.Fatal("incomplete revocation audit was accepted")
		}
	}
}

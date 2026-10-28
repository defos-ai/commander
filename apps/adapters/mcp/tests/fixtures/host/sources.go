package host

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
)

func AgentSources(t *testing.T) (*fsagents.AgentSourceFsStore, []agents.AgentSource) {
	t.Helper()
	store, err := fsagents.NewAgentSourceFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
	entries := []agents.AgentSource{
		{Name: "zeta", Description: "Second public source fixture.", Source: "github.com/acme/zeta-agent@stable", Visibility: packages.Public},
		{Name: "alpha", Description: "First private source fixture.", Source: "github.com/private-org/alpha-agent@v1.2.3", Visibility: packages.Private},
		{Name: "gamma", Description: "Second private source fixture.", Source: "github.com/private-org/gamma-agent@stable", Visibility: packages.Private},
		{Name: "beta", Description: "First public source fixture.", Source: "github.com/acme/beta-agent@v2.0.0", Visibility: packages.Public},
		{Name: "beta", Description: "Trusted public source fixture.", Source: "github.com/acme/beta-agent@v1.2.3", Visibility: packages.Public, Trusted: true, TrustUpdatedBy: "fixture-operator", TrustUpdatedAt: now},
	}
	for index := range entries {
		entries[index].Version = 1
		entries[index].CreatedAt = now
		entries[index].UpdatedAt = now
		if err := store.Create(t.Context(), entries[index]); err != nil {
			t.Fatal(err)
		}
	}
	return store, entries
}

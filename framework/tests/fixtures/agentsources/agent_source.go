package agentsources

import (
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func Source() agents.AgentSource {
	created := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	return agents.AgentSource{
		Name: "alpha", Description: "Inspect public systems.", Source: "github.com/example/alpha-agent@stable",
		Visibility: packages.Public, Version: 1, CreatedAt: created, UpdatedAt: created,
	}
}

func TrustedSource() agents.AgentSource {
	entry := Source()
	entry.Source = "github.com/example/alpha-agent@v1.2.3"
	entry.Version = 2
	entry.Trusted = true
	entry.UpdatedAt = entry.CreatedAt.Add(time.Second)
	entry.TrustUpdatedBy = "source-operator"
	entry.TrustUpdatedAt = entry.UpdatedAt
	return entry
}

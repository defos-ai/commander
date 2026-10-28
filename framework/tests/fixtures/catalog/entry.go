package catalog

import (
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func Entry() agents.CatalogEntry {
	created := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	return agents.CatalogEntry{
		Name: "alpha", Description: "Inspect public systems.", Package: "github.com/example/alpha-agent@stable",
		Visibility: packages.Public, Version: 1, CreatedAt: created, UpdatedAt: created,
	}
}

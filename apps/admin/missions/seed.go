package missions

import (
	"context"
	_ "embed"

	"github.com/defos-ai/commander/apps/admin/client"
)

//go:embed default-missions.json
var bundledMissions []byte

// Seed creates missing bundled missions through the API, preserving saved definitions.
func Seed(ctx context.Context, api *client.Client, dryRun bool) (ImportResult, error) {
	definitions, err := Parse(bundledMissions)
	if err != nil {
		return ImportResult{}, err
	}
	return Import(ctx, api, definitions, dryRun)
}

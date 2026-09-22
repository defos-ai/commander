package missions

import (
	"context"
)

// MissionStore holds reusable missions. Each operation keeps its own snapshot;
// editing the catalog affects future operations only.
type MissionStore interface {
	Upsert(context.Context, Mission) error
	Get(context.Context, string) (Mission, error)
	List(context.Context) ([]Mission, error)
}

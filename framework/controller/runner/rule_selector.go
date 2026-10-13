package runner

import (
	"context"
	"encoding/json"

	"github.com/defos-ai/commander/framework/domain/missions"
)

// RuleSelector selects the complete rule set for a new operation. A nil or empty
// result removes explicit rules; it does not fall back to or merge mission rules.
// Calls run outside the owner loop and may overlap; implementations must be
// concurrency-safe and honor ctx. The runner validates and snapshots the result.
// Recovery and task execution use that saved snapshot without selecting again.
type RuleSelector interface {
	Select(ctx context.Context, selection RuleSelection) (missions.RuleSet, error)
}

type RuleSelection struct {
	Mission missions.Mission
	Input   json.RawMessage
}

type MissionRuleSelector struct{}

func (MissionRuleSelector) Select(ctx context.Context, selection RuleSelection) (missions.RuleSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return selection.Mission.Rules.Clone(), nil
}

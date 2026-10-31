package sessionworker

import (
	"context"
	"encoding/json"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/observations"
	"time"
)

type AssessmentProbe struct{ *action.Base }

func NewAssessmentProbe() *AssessmentProbe {
	return &AssessmentProbe{Base: action.NewBase(actions.Action{Name: "assessment-probe", InputSchema: json.RawMessage(`{"type":"object","required":["destination"],"properties":{"destination":{"type":"string","x-defos-destination":true},"next_destination":{"type":"string"}},"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object"}`)})}
}
func (a *AssessmentProbe) Run(ctx context.Context, input json.RawMessage) (action.Result, error) {
	var value struct {
		Destination string `json:"destination"`
		Next        string `json:"next_destination"`
	}
	if err := json.Unmarshal(input, &value); err != nil {
		return action.Result{}, err
	}
	scope := action.AssessmentFromContext(ctx)
	revision := 0
	if scope != nil {
		revision = scope.Revision
	}
	result, err := action.JSONResult(map[string]any{"destination": value.Destination, "scope_revision": revision})
	if err != nil {
		return result, err
	}
	at, port := time.Now().UTC(), 443
	result.Observations = &observations.Batch{Version: 1, Records: []observations.Record{{ID: "fixture-observation", Kind: "service", Address: value.Destination, Port: &port, Transport: "tcp", ObservedAt: &at, Evidence: "Assessment fixture response"}}}
	if value.Next != "" {
		next, _ := json.Marshal(map[string]string{"destination": value.Next})
		result.NextActions = []actions.NextAction{{ActionName: "assessment-probe", Input: next}}
	}
	return result, nil
}

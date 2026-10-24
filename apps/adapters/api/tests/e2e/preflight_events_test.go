package e2e_test

import (
	"testing"

	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestE2EOrdinaryTaskPreservesUnchangedPreflight(t *testing.T) {
	stack := newSessionStack(t, "")
	stack.worker(1)
	stack.request("POST", "/api/v1/missions", map[string]any{
		"name": "stable-preflight", "objectives": []string{"Execute ordinary work without changing readiness"},
		"required_actions": []string{"mark-owner"}, "starting_actions": []string{"mark-owner"},
		"input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"},
	}, 201, nil)
	op := stack.start("stable-preflight")
	before := stack.operation(op)
	if before.Preflight == nil || !before.Preflight.Ready {
		t.Fatal("mission was not ready before assigning work")
	}

	task := stack.assign(op, "mark-owner", map[string]any{})
	stack.finished(op, task.ID)
	after := stack.operation(op)
	var events struct{ Items []operations.Event }
	stack.request("GET", "/api/v1/operations/"+op+"/events", nil, 200, &events)
	for _, event := range events.Items {
		if event.Type == operations.PreflightUpdated {
			t.Fatalf("ordinary task published a readiness change without changed requirements: %s", event.Message)
		}
	}
	if after.Preflight == nil || !after.Preflight.Ready || after.Preflight.Version != before.Preflight.Version {
		t.Fatalf("ordinary task changed preflight: before=%+v after=%+v", before.Preflight, after.Preflight)
	}
}

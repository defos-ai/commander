package e2e_test

import (
	"encoding/json"
	"testing"
	"time"

	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

func createSession(t *testing.T, s *e2efixtures.SessionStack, op string) sessions.Session {
	t.Helper()
	task := s.Assign(op, "connect-resource", map[string]any{})
	task = s.Finished(op, task.ID)
	if task.Result == nil || task.Result.Session == nil {
		t.Fatalf("missing session result: %+v", task)
	}
	var output struct {
		Immutable bool `json:"checkpoint_immutable"`
	}
	if err := json.Unmarshal(task.Result.Output, &output); err != nil || !output.Immutable {
		t.Fatal("creation did not preserve immutable checkpoints")
	}
	ref := *task.Result.Session
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		for _, item := range s.List(ref.ScopeID) {
			if item.ID == ref.ID && item.State == "connected" {
				return true
			}
		}
		return false
	})
	return ref
}

package e2e_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

func TestE2EAutomaticSessionBindingsSurviveRestartBeforeInventory(t *testing.T) {
	entered := make(chan string, 8)
	release, publish := make(chan struct{}), make(chan struct{})
	var releaseOnce, publishOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	publishInventory := func() { publishOnce.Do(func() { close(publish) }) }
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/inventory":
			select {
			case <-publish:
				w.WriteHeader(http.StatusNoContent)
			case <-r.Context().Done():
			}
		case strings.HasPrefix(r.URL.Path, "/create/"):
			entered <- r.URL.Path
			select {
			case <-release:
				w.WriteHeader(http.StatusNoContent)
			case <-r.Context().Done():
			}
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(peer.Close)
	t.Cleanup(unblock)
	t.Cleanup(publishInventory)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Worker(2)
	s.Mission("automatic-sessions", "propose-connections", "connect-resource")
	op := s.Start("automatic-sessions")

	parent := s.Assign(op, "propose-connections", map[string]any{})
	paths := map[string]bool{}
	for range 2 {
		select {
		case path := <-entered:
			if paths[path] {
				t.Fatalf("session effect repeated before restart: %s", path)
			}
			paths[path] = true
		case <-time.After(10 * time.Second):
			t.Fatal("automatic sibling did not open its own resource")
		}
	}
	accepted := map[string]operations.Task{}
	scopes, owners := map[string]bool{}, map[string]bool{}
	for _, task := range s.Operation(op).Tasks {
		if task.ParentTaskID != parent.ID {
			continue
		}
		intent := task.SessionIntent
		if intent == nil || intent.Mode != sessions.Open || intent.ID != task.CommandID || intent.ScopeID != task.CommandID {
			t.Fatalf("automatic child lacks its controller-assigned identity: %+v", task)
		}
		if scopes[intent.ScopeID] {
			t.Fatalf("sibling openings share a scope: %+v", task)
		}
		if !paths["/create/"+task.AgentID+"/"+intent.ID] {
			t.Fatalf("resource effect does not match its accepted binding: %+v", task)
		}
		scopes[intent.ScopeID], owners[task.AgentID] = true, true
		accepted[task.ID] = task
	}
	if len(accepted) != 2 || len(owners) != 2 {
		t.Fatalf("siblings did not acquire independent sessions and owners: %+v", accepted)
	}
	s.Restart()

	for taskID, original := range accepted {
		task := s.Task(op, taskID)
		if task.SessionIntent == nil || task.CommandID != original.CommandID || task.AgentID != original.AgentID || *task.SessionIntent != *original.SessionIntent {
			t.Fatalf("restart changed an accepted session binding: before=%+v after=%+v", original, task)
		}
	}
	unblock()
	for taskID := range accepted {
		task := s.Finished(op, taskID)
		if task.Result == nil || task.Result.Session == nil || task.Result.Session.ID != task.SessionIntent.ID {
			t.Fatalf("accepted session result lost its assigned identity: %+v", task)
		}
	}
	s.Restart()
	for taskID, original := range accepted {
		task := s.Task(op, taskID)
		if task.SessionIntent == nil || task.CommandID != original.CommandID || task.AgentID != original.AgentID || *task.SessionIntent != *original.SessionIntent {
			t.Fatalf("result recovery changed an accepted binding: before=%+v after=%+v", original, task)
		}
		items := s.List(task.SessionIntent.ScopeID)
		if len(items) != 1 || items[0].ID != task.SessionIntent.ID || items[0].OwnerAgentID != task.AgentID {
			t.Fatalf("accepted result did not retain ownership before inventory: %+v", items)
		}
	}
	publishInventory()
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		var catalog struct {
			Items []struct{ Sessions []sessions.Session }
		}
		s.Request("GET", "/api/v1/agents", nil, 200, &catalog)
		count := 0
		for _, worker := range catalog.Items {
			count += len(worker.Sessions)
		}
		return count == 2
	})
	select {
	case repeated := <-entered:
		t.Fatalf("recovery repeated a resource effect: %s", repeated)
	default:
	}
	if len(s.Operation(op).Tasks) != 3 {
		t.Fatal("recovery duplicated the automatic sibling tasks")
	}
}

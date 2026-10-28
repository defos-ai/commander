package e2e_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func TestE2EAcceptedSessionCommandsSurviveReconciliationFailure(t *testing.T) {
	e2efixtures.RequireFilesystemFixture(t)
	var unavailable atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sessions" && unavailable.Load() {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(peer.Close)
	s := e2efixtures.NewSessionStack(t, peer.URL, "./apps/adapters/api/tests/e2e/fixtures/faultcontroller")
	s.Worker(1)
	s.Mission("accepted-session", false, "connect-resource", "mark-owner")
	for _, name := range []string{"assign", "resolve-continuation", "cancel", "fail"} {
		t.Run(name, func(t *testing.T) {
			continuation := name == "resolve-continuation"
			restore := s.WithTest(t)
			defer restore()
			op := s.Start("accepted-session")
			var parent operations.Task
			if continuation {
				parent = s.Assign(op, "mark-owner", map[string]any{})
				parent = s.Finished(op, parent.ID)
			}
			unavailable.Store(true)
			t.Cleanup(func() { unavailable.Store(false) })
			var child operations.Task
			input := map[string]string{"session_scope": id.New()}
			if continuation {
				var accepted operations.Operation
				s.Request("POST", "/api/v1/operations/"+op+"/tasks/"+parent.ID+"/continuation/resolve", map[string]any{"version": parent.Continuation.Version, "actions": []any{map[string]any{"action_name": "connect-resource", "input": input}}}, 200, &accepted)
				for _, task := range accepted.Tasks {
					if task.ParentTaskID == parent.ID {
						child = task
					}
				}
			} else {
				child = s.Assign(op, "connect-resource", input)
			}
			if child.ID == "" {
				t.Fatal("accepted command did not return its task")
			}
			if name == "cancel" || name == "fail" {
				body := map[string]any{}
				if name == "fail" {
					body["reason"] = "E2E deliberate failure"
				}
				s.Request("POST", "/api/v1/operations/"+op+"/"+name, body, 200, nil)
				unavailable.Store(false)
				// Terminal operations must still reconcile and release a creation
				// that was cancelled before dispatch, allowing fresh work in its scope.
				e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
					for _, item := range s.List(input["session_scope"]) {
						if item.ID == child.ID && item.Disposition == "released" {
							return true
						}
					}
					return false
				})
				fresh := s.Start("accepted-session")
				replacement := s.Assign(fresh, "connect-resource", input)
				s.Finished(fresh, replacement.ID)
				return
			}
			unavailable.Store(false)
			s.Finished(op, child.ID)
			count := 0
			for _, task := range s.Operation(op).Tasks {
				if task.ActionName == "connect-resource" {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("accepted command created %d tasks", count)
			}
		})
	}
}

func TestE2EProposalsSurviveCatalogOutage(t *testing.T) {
	e2efixtures.RequireFilesystemFixture(t)
	for _, target := range []string{"mark-owner", "unregistered-action"} {
		t.Run(target, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var unavailable atomic.Bool
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/catalog" && unavailable.Load() {
					w.WriteHeader(503)
					return
				}
				if r.URL.Path == "/propose" {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
					}
				}
				w.WriteHeader(204)
			}))
			t.Cleanup(peer.Close)
			t.Cleanup(unblock)
			s := e2efixtures.NewSessionStack(t, peer.URL, "./apps/adapters/api/tests/e2e/fixtures/faultcontroller")
			s.Worker(1)
			s.Mission("catalog-outage", false, "connect-resource", "propose-work")
			op := s.Start("catalog-outage")
			parent := s.Assign(op, "propose-work", map[string]string{"action_name": target})
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("proposal did not start")
			}
			unavailable.Store(true)
			restore := func() { unavailable.Store(false) }
			t.Cleanup(restore)
			unblock()
			if target == "unregistered-action" {
				// Observe the API throughout the outage: a lookup failure must not
				// turn the proposal into a permanently invalid saved child.
				deadline := time.Now().Add(time.Second)
				for time.Now().Before(deadline) {
					if len(s.Operation(op).Tasks) != 1 {
						t.Fatal("catalog outage permanently classified an unresolved proposal")
					}
					time.Sleep(25 * time.Millisecond)
				}
				// The completed execution remains recoverable while operation-level
				// acceptance waits for contracts; restart must not rerun the parent.
				s.Restart()
				if len(s.Operation(op).Tasks) != 1 {
					t.Fatal("restart classified a proposal while its catalog was unavailable")
				}
				restore()
			}
			s.Finished(op, parent.ID)
			var child operations.Task
			for _, task := range s.Operation(op).Tasks {
				if task.ParentTaskID == parent.ID {
					child = task
				}
			}
			if child.ID == "" {
				t.Fatal("proposal was lost")
			}
			if target == "mark-owner" {
				if child.SessionIntent != nil {
					t.Fatalf("known action acquired an admission error: %+v", child.SessionIntent)
				}
				s.Finished(op, child.ID)
			} else if child.SessionIntent == nil || child.SessionIntent.Error == "" {
				t.Fatal("unknown action did not retain its admission error")
			}
		})
	}
}

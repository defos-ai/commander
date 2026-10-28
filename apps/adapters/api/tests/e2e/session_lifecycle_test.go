package e2e_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func TestE2ESessionLifecycleOwnership(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Mission("lifecycle", true, "connect-resource")
	op1 := s.Start("lifecycle")
	scope := id.New()
	first := createSession(t, s, op1, scope)
	// Completed creation work does not own the resource's lifetime.
	s.Request("POST", "/api/v1/operations/"+op1+"/complete", map[string]any{"output": map[string]any{}}, 200, nil)
	s.Worker(2)
	op2 := s.Start("lifecycle")
	use := s.Assign(op2, "use-resource", map[string]string{"session_scope": scope, "session_id": first.ID})
	used := s.Finished(op2, use.ID)
	var output struct {
		Owner        string
		Isolated     bool   `json:"checkpoint_isolated"`
		CreationTask string `json:"creation_task"`
	}
	if err := json.Unmarshal(used.Result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if used.AgentID != first.OwnerAgentID || !output.Isolated || output.CreationTask == use.ID {
		t.Fatalf("session binding/checkpoints escaped execution scope: %+v %+v", used, output)
	}
	// Explicit abandonment releases a lost scope; it leaves the old operation bound.
	if err := s.SignalWorker(1, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	e2efixtures.WaitUntil(t, 12*time.Second, func() bool {
		for _, v := range s.List(scope) {
			if v.ID == first.ID {
				return v.State == "lost"
			}
		}
		return false
	})
	abandoned := s.Control(first.Reference, "abandon")
	if abandoned.ReleaseReason != "abandoned" || abandoned.State != "lost" {
		t.Fatalf("abandonment claimed closure: %+v", abandoned)
	}
	op3 := s.Start("lifecycle")
	replacement := createSession(t, s, op3, scope)
	if replacement.ID == first.ID || replacement.OwnerAgentID == first.OwnerAgentID {
		t.Fatalf("replacement reused the old identity: %+v", replacement)
	}
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		op := s.Operation(op2)
		for _, r := range op.Preflight.Actions {
			if len(r.ReadyAgentIDs) > 0 {
				return false
			}
		}
		return true
	})
	blocked := s.Assign(op2, "use-resource", map[string]string{"session_scope": scope, "session_id": replacement.ID})
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		op := s.Operation(op2)
		if op.Preflight == nil {
			return false
		}
		for _, p := range op.Preflight.Tasks {
			if p.TaskID == blocked.ID {
				return strings.Contains(p.Reason, "placement_conflict")
			}
		}
		return false
	})
	if task := s.Task(op2, blocked.ID); task.AgentID != "" || task.Status != operations.TaskQueued {
		t.Fatalf("abandonment rebound old affinity: %+v", task)
	}
	s.Restart()
	items := s.List(scope)
	if len(items) != 2 {
		t.Fatalf("history lost after restart: %+v", items)
	}
	for _, item := range items {
		if item.ID == first.ID && item.ReleaseReason != "abandoned" {
			t.Fatalf("old identity reclaimed scope: %+v", item)
		}
	}
	if err := s.SignalWorker(1, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	e2efixtures.WaitUntil(t, 8*time.Second, func() bool {
		var catalog struct {
			Items []struct {
				ID       string
				Sessions []sessions.Session
			}
		}
		s.Request("GET", "/api/v1/agents", nil, 200, &catalog)
		for _, a := range catalog.Items {
			if a.ID == first.OwnerAgentID {
				for _, v := range a.Sessions {
					if v.ID == first.ID && v.State == "closed" {
						return true
					}
				}
			}
		}
		return false
	})
	for _, item := range s.List(scope) {
		if item.ID == first.ID && (item.ReleaseReason != "abandoned" || item.State != "lost") {
			t.Fatalf("late owner reclaimed abandoned scope: %+v", item)
		}
	}
	closed := s.Control(replacement.Reference, "close")
	if closed.ReleaseReason != "closed" {
		t.Fatalf("close was not confirmed durably: %+v", closed)
	}
	s.Restart()
	op4 := s.Start("lifecycle")
	fresh := createSession(t, s, op4, scope)
	if fresh.ID == replacement.ID {
		t.Fatal("closed session identity reused")
	}
	otherScope := id.New()
	other := createSession(t, s, s.Start("lifecycle"), otherScope)
	var discovered struct{ Items []sessions.Session }
	s.Request("GET", "/api/v1/sessions", nil, 200, &discovered)
	if len(discovered.Items) != 4 {
		t.Fatalf("discovery lost retained or cross-scope sessions: %+v", discovered.Items)
	}
	for i, item := range discovered.Items {
		if i > 0 {
			previous := discovered.Items[i-1]
			if previous.ScopeID > item.ScopeID || previous.ScopeID == item.ScopeID && previous.ID >= item.ID {
				t.Fatal("session discovery order is not stable by scope and identity")
			}
		}
		var detail sessions.Session
		s.Request("GET", "/api/v1/sessions/"+item.ID, nil, 200, &detail)
		if detail.Reference != item.Reference || detail.Disposition != item.Disposition || detail.ReleaseReason != item.ReleaseReason {
			t.Fatalf("ID lookup changed retained ownership/lifecycle: listed=%+v detail=%+v", item, detail)
		}
		if item.ID == first.ID && (detail.State != "lost" || detail.ReleaseReason != "abandoned") ||
			item.ID == replacement.ID && (detail.State != "closed" || detail.ReleaseReason != "closed") {
			t.Fatalf("ID lookup lost released history: %+v", detail)
		}
	}
	for _, filter := range []struct{ scope, owner string }{
		{scope: scope}, {owner: first.OwnerAgentID}, {scope: scope, owner: first.OwnerAgentID},
		{scope: otherScope, owner: other.OwnerAgentID}, {scope: id.New()}, {owner: "fixture-agent-ffffffff"},
	} {
		var want []string
		for _, item := range discovered.Items {
			if (filter.scope == "" || item.ScopeID == filter.scope) && (filter.owner == "" || item.OwnerAgentID == filter.owner) {
				want = append(want, item.ID)
			}
		}
		var filtered struct{ Items []sessions.Session }
		s.Request("GET", "/api/v1/sessions?"+e2efixtures.SessionFilter(filter.scope, filter.owner), nil, 200, &filtered)
		var got []string
		for _, item := range filtered.Items {
			got = append(got, item.ID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") || filtered.Items == nil {
			t.Fatalf("session filters did not intersect: filter=%+v got=%v want=%v", filter, got, want)
		}
	}
}

func TestE2ESessionAffinityLookaheadAndOwnerReadiness(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1, "FIXTURE_USE_REQUIRES_SECRET=true")
	s.Mission("create-only", false, "connect-resource")
	scope := id.New()
	resource := createSession(t, s, s.Start("create-only"), scope)
	s.Worker(2)
	s.Mission("lookahead", true, "connect-resource")
	op := s.Start("lookahead")
	parent := s.Assign(op, "propose-affinity", map[string]string{"session_scope": scope, "session_id": resource.ID})
	s.Finished(op, parent.ID)
	// B advertises a ready use action, but only A owns the selected session.
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		op := s.Operation(op)
		if op.Preflight == nil || op.Preflight.Ready {
			return false
		}
		for _, a := range op.Preflight.Actions {
			if a.ActionName != "use-resource" {
				continue
			}
			if len(a.ReadyAgentIDs) > 0 {
				t.Fatal("non-owner readiness masked owner requirements")
			}
			for _, blocked := range a.UnavailableAgents {
				if blocked.AgentID == resource.OwnerAgentID && len(blocked.MissingRequirements) > 0 {
					return true
				}
			}
		}
		return false
	})
	current := s.Operation(op)
	for _, task := range current.Tasks {
		if task.ActionName == "mark-owner" && task.AgentID != "" {
			t.Fatalf("stateless task bound group before owner became ready: %+v", task)
		}
	}
	var agents struct {
		Items []struct {
			Actions []struct {
				Name string `json:"name"`
				Mode string `json:"session_mode"`
			}
		}
	}
	s.Request("GET", "/api/v1/agents", nil, 200, &agents)
	found := 0
	for _, a := range agents.Items {
		for _, action := range a.Actions {
			if action.Name == "use-resource" && action.Mode == "use" {
				found++
			}
		}
	}
	if found != 2 {
		t.Fatalf("typed session metadata lost: %+v", agents)
	}
}

func TestE2ESessionCloseDuringCreation(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/create/") {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(peer.Close)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Mission("close-prepared", false, "connect-resource")
	op := s.Start("close-prepared")
	scope := id.New()
	task := s.Assign(op, "connect-resource", map[string]string{"session_scope": scope})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("creation never started")
	}
	var prepared sessions.Session
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		items := s.List(scope)
		if len(items) == 1 {
			prepared = items[0]
			return prepared.OwnerAgentID != ""
		}
		return false
	})
	closed := s.Control(prepared.Reference, "close")
	if closed.State != "closed" || closed.ReleaseReason != "closed" {
		t.Fatalf("missing closure receipt: %+v", closed)
	}
	once.Do(func() { close(release) })
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool { return s.Task(op, task.ID).Status == operations.TaskFailed })
	s.Restart()
	items := s.List(scope)
	if len(items) != 1 || items[0].ReleaseReason != "closed" {
		t.Fatalf("late creation observation reopened closed claim: %+v", items)
	}
	replacement := createSession(t, s, s.Start("close-prepared"), scope)
	if replacement.ID == prepared.ID {
		t.Fatal("replacement inherited closed identity")
	}
}

func TestE2ESessionCancellationReleasesUnstartedScope(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Mission("blocked-create", false, "connect-resource", "missing-capability")
	s.Mission("available-create", false, "connect-resource")
	for _, method := range []string{"cancel", "fail"} {
		t.Run(method, func(t *testing.T) {
			scope := id.New()
			op := s.Start("blocked-create")
			task := s.Assign(op, "connect-resource", map[string]string{"session_scope": scope})
			current := s.Task(op, task.ID)
			if current.Status != operations.TaskQueued || current.AgentID != "" {
				t.Fatalf("expected undispatched reservation: %+v", current)
			}
			payload := map[string]any{}
			if method == "fail" {
				payload["reason"] = "fixture stopping queued work"
			}
			s.Request("POST", "/api/v1/operations/"+op+"/"+method, payload, 200, nil)
			items := s.List(scope)
			if len(items) != 1 || items[0].ReleaseReason != "cancelled_before_effect" {
				t.Fatalf("unstarted reservation retained: %+v", items)
			}
			replacement := createSession(t, s, s.Start("available-create"), scope)
			if replacement.ID == task.ID {
				t.Fatal("cancelled identity reused")
			}
		})
	}
}

func TestE2ESessionGroupLookaheadAndIndependentOwners(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Mission("ungrouped", false, "connect-resource")
	firstScope := id.New()
	first := createSession(t, s, s.Start("ungrouped"), firstScope)
	s.Worker(2, "FIXTURE_NO_PROPOSER=true")
	secondScope := id.New()
	second := createSession(t, s, s.Start("ungrouped"), secondScope)
	if first.OwnerAgentID == second.OwnerAgentID {
		t.Fatal("fixture needs two different resource owners")
	}
	s.Mission("grouped", true, "connect-resource")
	op := s.Start("grouped")
	parent := s.Assign(op, "propose-affinity", map[string]string{"session_scope": firstScope, "session_id": first.ID})
	s.Finished(op, parent.ID)
	e2efixtures.WaitUntil(t, 10*time.Second, func() bool {
		current := s.Operation(op)
		done := 0
		for _, task := range current.Tasks {
			if task.ParentTaskID == parent.ID {
				if task.AgentID != "" && task.AgentID != first.OwnerAgentID {
					t.Fatalf("group bound away from later accepted session owner: %+v", task)
				}
				if task.Status == operations.TaskSucceeded {
					done++
				}
			}
		}
		return done == 2
	})
	// No same-agent rule means different tasks can use different exact owners.
	separate := s.Start("ungrouped")
	a := s.Assign(separate, "use-resource", map[string]string{"session_scope": firstScope, "session_id": first.ID})
	b := s.Assign(separate, "use-resource", map[string]string{"session_scope": secondScope, "session_id": second.ID})
	if s.Finished(separate, a.ID).AgentID != first.OwnerAgentID || s.Finished(separate, b.ID).AgentID != second.OwnerAgentID {
		t.Fatal("independent sessions gained an implicit affinity group")
	}
}

func TestE2ESessionReuseBeforeVerification(t *testing.T) {
	var creations atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/create/") {
			creations.Add(1)
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(peer.Close)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1, "FIXTURE_SESSION_STATE=listening")
	s.Mission("reuse", false, "connect-resource")
	op, scope := s.Start("reuse"), id.New()
	created := s.Assign(op, "connect-resource", map[string]string{"session_scope": scope})
	result := s.Finished(op, created.ID)
	ref := result.Result.Session.Reference
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		items := s.List(scope)
		return len(items) == 1 && items[0].State == "listening"
	})
	s.Worker(2)
	op2 := s.Start("reuse")
	input := map[string]string{"session_scope": scope, "session_id": ref.ID}
	use := s.Assign(op2, "use-resource", input)
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		for _, task := range s.Operation(op2).Preflight.Tasks {
			if task.TaskID == use.ID && strings.Contains(task.Reason, "session_not_usable") {
				return true
			}
		}
		return false
	})
	reuse := s.Assign(op2, "connect-resource", input)
	reused := s.Finished(op2, reuse.ID)
	if reused.SessionIntent.Mode != sessions.Reuse || reused.AgentID != ref.OwnerAgentID || reused.Result.Session.Reference != ref || creations.Load() != 1 {
		t.Fatalf("explicit reuse opened a replacement: %+v", reused)
	}
	s.Control(ref, "verify")
	if s.Finished(op2, use.ID).AgentID != ref.OwnerAgentID {
		t.Fatal("verified session use moved owners")
	}
}

func TestE2ESessionCloseReceiptRecovery(t *testing.T) {
	var allowClose atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/close/") && !allowClose.Load() {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(peer.Close)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Mission("close-recovery", false, "connect-resource")
	scope := id.New()
	first := createSession(t, s, s.Start("close-recovery"), scope)
	request := map[string]any{"id": first.ID, "scope_id": scope, "owner_agent_id": first.OwnerAgentID, "method": "close", "request_id": id.New()}
	s.Request("POST", "/api/v1/sessions/control", request, 422, nil)
	items := s.List(scope)
	if len(items) != 1 || items[0].Disposition != "closing" || items[0].ReleaseReason != "" {
		t.Fatalf("failed close released the scope: %+v", items)
	}
	op := s.Start("close-recovery")
	s.Request("POST", "/api/v1/operations/"+op+"/tasks", map[string]any{"action_name": "connect-resource", "input": map[string]string{"session_scope": scope}}, 422, nil)
	s.Restart()
	allowClose.Store(true)
	e2efixtures.WaitUntil(t, 10*time.Second, func() bool {
		items := s.List(scope)
		return len(items) == 1 && items[0].ReleaseReason == "closed"
	})
	if replacement := createSession(t, s, op, scope); replacement.ID == first.ID {
		t.Fatal("confirmed replacement reused the old reference")
	}
}

func TestE2ESessionIntentBeforeRegistration(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	contract := map[string]any{"name": "connect-resource", "session_mode": "create", "input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"}}
	s.Request("POST", "/api/v1/missions", map[string]any{"name": "before-registration", "objectives": []string{"Accept a session contract before its executor arrives"}, "required_actions": []string{"connect-resource"}, "starting_actions": []string{"connect-resource"}, "action_definitions": []any{contract}, "input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"}}, 201, nil)
	op, scope := s.Start("before-registration"), id.New()
	task := s.Assign(op, "connect-resource", map[string]string{"session_scope": scope})
	items := s.List(scope)
	if len(items) != 1 || items[0].ID != task.ID || items[0].OwnerAgentID != "" || items[0].Disposition != "reserved" {
		t.Fatalf("missing unowned durable admission: %+v", items)
	}
	s.Restart()
	s.Worker(1)
	finished := s.Finished(op, task.ID)
	if finished.SessionIntent.Mode != sessions.Create || finished.Result.Session.ID != task.ID {
		t.Fatalf("registration reinterpreted accepted session intent: %+v", finished)
	}
}

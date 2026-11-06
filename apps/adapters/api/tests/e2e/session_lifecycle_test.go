package e2e_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	e2efixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/e2e"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func TestE2ESessionOwnerSurvivesControllerDowntime(t *testing.T) {
	inventoryEntered := make(chan struct{}, 1)
	releaseInventory := make(chan struct{})
	var released sync.Once
	var creations atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/create/") {
			creations.Add(1)
		}
		if r.URL.Path == "/inventory" {
			select {
			case inventoryEntered <- struct{}{}:
			default:
			}
			select {
			case <-releaseInventory:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(peer.Close)
	t.Cleanup(func() { released.Do(func() { close(releaseInventory) }) })
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Mission("controller-downtime", "connect-resource")
	op := s.Start("controller-downtime")
	resource := createSession(t, s, op)
	select {
	case <-inventoryEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("session inventory was never held")
	}

	s.StopController()
	logOffset := len(s.WorkerLog(1))
	released.Do(func() { close(releaseInventory) })
	amqpBus := strings.HasPrefix(os.Getenv("COMMANDER_SESSION_E2E_BUS_URL"), "amqp")
	heartbeat := "heartbeat sent"
	if amqpBus {
		heartbeat = "heartbeat failed"
	}
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		return strings.Contains(s.WorkerLog(1)[logOffset:], heartbeat)
	})
	if amqpBus {
		e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
			return strings.Contains(s.WorkerLog(1)[logOffset:], "session state announcement failed")
		})
	}
	s.Restart()
	use := s.Continue(s.Task(op, resource.ID), "use-resource", map[string]any{})
	finished := s.Finished(op, use.ID)

	if finished.AgentID != resource.OwnerAgentID || finished.SessionIntent == nil || finished.SessionIntent.ID != resource.ID || creations.Load() != 1 {
		t.Fatalf("controller downtime replaced the session or owner: task=%+v creations=%d", finished, creations.Load())
	}
	s.Request("POST", "/api/v1/operations/"+op+"/complete", map[string]any{"output": map[string]any{}}, 200, nil)
	s.Restart()
	items := s.List(resource.ScopeID)
	if len(items) != 1 || items[0].Reference != resource.Reference || items[0].State != "connected" || creations.Load() != 1 {
		t.Fatalf("operation completion changed resource lifetime: %+v", items)
	}
}

func TestE2ESessionLifecycleOwnership(t *testing.T) {
	peer, network := e2efixtures.PartitionableSessionPeer(t)
	defer network.Resume()
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1, "FIXTURE_PARTITION_CHECK=true")
	s.Mission("lifecycle", "connect-resource")
	op1 := s.Start("lifecycle")
	first := createSession(t, s, op1)
	scope := first.ScopeID
	s.Worker(2)
	use := s.Continue(s.Task(op1, first.ID), "use-resource", map[string]any{})
	used := s.Finished(op1, use.ID)
	var output struct {
		Owner        string
		Isolated     bool   `json:"checkpoint_isolated"`
		CreationTask string `json:"creation_task"`
	}
	if err := json.Unmarshal(used.Result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if used.AgentID != first.OwnerAgentID || output.Owner != first.OwnerAgentID || !output.Isolated || output.CreationTask != first.ID {
		t.Fatalf("session binding/checkpoints escaped execution scope: %+v %+v", used, output)
	}
	// Abandonment retains old bindings even when new work opens another resource.
	network.Pause()
	e2efixtures.WaitUntil(t, 12*time.Second, func() bool {
		for _, v := range s.List(scope) {
			if v.ID == first.ID {
				return v.State == "lost"
			}
		}
		return false
	})
	blocked := s.Continue(used, "use-resource", map[string]any{})
	abandoned := s.Control(first.Reference, "abandon")
	if abandoned.ReleaseReason != "abandoned" || abandoned.State != "lost" {
		t.Fatalf("abandonment claimed closure: %+v", abandoned)
	}
	op3 := s.Start("lifecycle")
	replacement := createSession(t, s, op3)
	if replacement.ID == first.ID || replacement.OwnerAgentID == first.OwnerAgentID {
		t.Fatalf("replacement reused the old identity: %+v", replacement)
	}
	reuse := s.Continue(s.Task(op3, replacement.ID), "use-resource", map[string]any{})
	if finished := s.Finished(op3, reuse.ID); finished.AgentID != replacement.OwnerAgentID {
		t.Fatalf("replacement session was routed to another owner: %+v", finished)
	}
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		op := s.Operation(op1)
		if op.Preflight == nil {
			return false
		}
		for _, placement := range op.Preflight.Tasks {
			if placement.TaskID == blocked.ID {
				return strings.Contains(placement.Reason, "session_abandoned")
			}
		}
		return false
	})
	if task := s.Task(op1, blocked.ID); task.AgentID != "" || task.Status != operations.TaskQueued || task.SessionIntent == nil || task.SessionIntent.ID != first.ID {
		t.Fatalf("abandonment redirected work from the old session: %+v", task)
	}
	s.Restart()
	items := s.List(scope)
	if len(items) != 1 {
		t.Fatalf("history lost after restart: %+v", items)
	}
	for _, item := range items {
		if item.ID == first.ID && item.ReleaseReason != "abandoned" {
			t.Fatalf("old identity reclaimed scope: %+v", item)
		}
	}
	network.Resume()
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
	fresh := createSession(t, s, op4)
	if fresh.ID == replacement.ID || fresh.ScopeID == replacement.ScopeID {
		t.Fatal("closed session identity reused")
	}
	other := createSession(t, s, s.Start("lifecycle"))
	otherScope := other.ScopeID
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

func TestE2ESessionOwnerReadiness(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1, "FIXTURE_USE_REQUIRES_SECRET=true")
	s.Mission("owner-readiness", "connect-resource")
	op := s.Start("owner-readiness")
	resource := createSession(t, s, op)
	s.Worker(2)
	task := s.Continue(s.Task(op, resource.ID), "use-resource", map[string]any{})
	// B advertises a ready use action, but only A owns the selected session.
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		op := s.Operation(op)
		if op.Preflight == nil || op.Preflight.Ready {
			return false
		}
		for _, placement := range op.Preflight.Tasks {
			if placement.TaskID == task.ID && placement.OwnerAgentID == resource.OwnerAgentID && len(placement.MissingRequirements) > 0 {
				return true
			}
		}
		return false
	})
	if current := s.Task(op, task.ID); current.AgentID != "" || current.Status != operations.TaskQueued {
		t.Fatalf("ready non-owner received a task requiring another session: %+v", current)
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
	s.Mission("close-prepared", "connect-resource")
	op := s.Start("close-prepared")
	task := s.Assign(op, "connect-resource", map[string]any{})
	scope := task.SessionIntent.ScopeID
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
	replacement := createSession(t, s, s.Start("close-prepared"))
	if replacement.ID == prepared.ID {
		t.Fatal("replacement inherited closed identity")
	}
}

func TestE2ESessionCancellationReleasesUnstartedScope(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Mission("blocked-create", "connect-resource", "missing-capability")
	s.Mission("available-create", "connect-resource")
	for _, method := range []string{"cancel", "fail"} {
		t.Run(method, func(t *testing.T) {
			restore := s.WithTest(t)
			defer restore()
			op := s.Start("blocked-create")
			task := s.Assign(op, "connect-resource", map[string]any{})
			scope := task.SessionIntent.ScopeID
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
			replacement := createSession(t, s, s.Start("available-create"))
			if replacement.ID == task.ID {
				t.Fatal("cancelled identity reused")
			}
		})
	}
}

func TestE2ESessionIndependentOwners(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	s.Worker(1)
	s.Mission("sessions", "connect-resource")
	op := s.Start("sessions")
	first := createSession(t, s, op)
	s.Worker(2)
	second := createSession(t, s, op)
	if first.OwnerAgentID == second.OwnerAgentID {
		t.Fatal("fixture needs two different resource owners")
	}
	for _, input := range []map[string]any{
		{}, {"session_scope": first.ScopeID, "session_id": first.ID},
	} {
		s.Request("POST", "/api/v1/operations/"+op+"/tasks", map[string]any{"action_name": "use-resource", "input": input}, 422, nil)
	}
	a := s.Continue(s.Task(op, first.ID), "use-resource", map[string]any{})
	b := s.Continue(s.Task(op, second.ID), "use-resource", map[string]any{})
	firstUse, secondUse := s.Finished(op, a.ID), s.Finished(op, b.ID)
	if firstUse.AgentID != first.OwnerAgentID || secondUse.AgentID != second.OwnerAgentID ||
		firstUse.SessionIntent == nil || secondUse.SessionIntent == nil ||
		firstUse.SessionIntent.ID != first.ID || secondUse.SessionIntent.ID != second.ID ||
		firstUse.SessionIntent.ScopeID == secondUse.SessionIntent.ScopeID {
		t.Fatal("tasks did not use their exact session owners")
	}
	stateless := s.Continue(firstUse, "mark-owner", map[string]any{})
	if finished := s.Finished(op, stateless.ID); finished.SessionIntent != nil {
		t.Fatalf("stateless continuation inherited a session: %+v", finished)
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
	s.Mission("reuse", "connect-resource")
	op := s.Start("reuse")
	created := s.Assign(op, "connect-resource", map[string]any{})
	result := s.Finished(op, created.ID)
	ref := result.Result.Session.Reference
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		items := s.List(ref.ScopeID)
		return len(items) == 1 && items[0].State == "listening"
	})
	s.Worker(2)
	reuse := s.Continue(result, "connect-resource", map[string]any{})
	reused := s.Finished(op, reuse.ID)
	if reused.SessionIntent.Mode != sessions.Open || reused.AgentID != ref.OwnerAgentID || reused.Result.Session.Reference != ref || creations.Load() != 1 {
		t.Fatalf("open continuation created a replacement: %+v", reused)
	}
	use := s.Continue(reused, "use-resource", map[string]any{})
	e2efixtures.WaitUntil(t, 5*time.Second, func() bool {
		for _, task := range s.Operation(op).Preflight.Tasks {
			if task.TaskID == use.ID && strings.Contains(task.Reason, "session_not_usable") {
				return true
			}
		}
		return false
	})
	s.Control(ref, "verify")
	if s.Finished(op, use.ID).AgentID != ref.OwnerAgentID {
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
	s.Mission("close-recovery", "connect-resource")
	op := s.Start("close-recovery")
	first := createSession(t, s, op)
	scope := first.ScopeID
	request := map[string]any{"id": first.ID, "scope_id": scope, "owner_agent_id": first.OwnerAgentID, "method": "close", "request_id": id.New()}
	s.Request("POST", "/api/v1/sessions/control", request, 422, nil)
	items := s.List(scope)
	if len(items) != 1 || items[0].Disposition != "closing" || items[0].ReleaseReason != "" {
		t.Fatalf("failed close released the scope: %+v", items)
	}
	creation := s.Task(op, first.ID)
	continuation := "/api/v1/operations/" + op + "/tasks/" + creation.ID + "/continuation/resolve"
	proposal := map[string]any{"version": creation.Continuation.Version, "actions": []any{map[string]any{"action_name": "connect-resource", "input": map[string]any{}}}}
	s.Request("POST", continuation, proposal, 422, nil)
	s.Restart()
	allowClose.Store(true)
	e2efixtures.WaitUntil(t, 10*time.Second, func() bool {
		items := s.List(scope)
		return len(items) == 1 && items[0].ReleaseReason == "closed"
	})
	s.Request("POST", continuation, proposal, 422, nil)
	if replacement := createSession(t, s, s.Start("close-recovery")); replacement.ID == first.ID || replacement.ScopeID == first.ScopeID {
		t.Fatal("confirmed replacement reused the old reference")
	}
}

func TestE2ESessionIntentBeforeRegistration(t *testing.T) {
	peer := e2efixtures.ImmediateSessionPeer(t)
	s := e2efixtures.NewSessionStack(t, peer.URL)
	contract := map[string]any{"name": "connect-resource", "session_mode": "open", "input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"}}
	s.Request("POST", "/api/v1/missions", map[string]any{"name": "before-registration", "objectives": []string{"Accept a session contract before its executor arrives"}, "required_actions": []string{"connect-resource"}, "starting_actions": []string{"connect-resource"}, "action_definitions": []any{contract}, "input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"}}, 201, nil)
	op := s.Start("before-registration")
	task := s.Assign(op, "connect-resource", map[string]any{})
	items := s.List(task.SessionIntent.ScopeID)
	if len(items) != 1 || items[0].ID != task.ID || items[0].OwnerAgentID != "" || items[0].Disposition != "reserved" {
		t.Fatalf("missing unowned durable admission: %+v", items)
	}
	s.Restart()
	s.Worker(1)
	finished := s.Finished(op, task.ID)
	if finished.SessionIntent.Mode != sessions.Open || finished.Result.Session.ID != task.ID {
		t.Fatalf("registration reinterpreted accepted session intent: %+v", finished)
	}
}

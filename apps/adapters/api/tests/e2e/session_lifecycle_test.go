package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

// These scenarios use public HTTP commands/views and executable hosts only.
// COMMANDER_SESSION_E2E_DATABASE_URL selects an isolated Mongo replica-set DB.
type sessionStack struct {
	processes           map[int]*os.Process
	t                   *testing.T
	url, password       string
	client              *http.Client
	env, controllerArgs []string
	binaries            map[string]string
	stopController      func()
	peer                string
}

func newSessionStack(t *testing.T, peer string, controllerSource ...string) *sessionStack {
	t.Helper()
	root, err := filepath.Abs("../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	binaries := map[string]string{}
	for name, source := range map[string]string{"api": "./apps/adapters/api/cmd/commander-api", "controller": "./apps/controllers/default/cmd", "worker": "./apps/adapters/api/tests/e2e/fixtures/sessionworker"} {
		if name == "controller" && len(controllerSource) != 0 {
			source = controllerSource[0]
		}
		binaries[name] = filepath.Join(state, name)
		command := exec.Command("go", "build", "-o", binaries[name], source)
		command.Dir = root
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, out)
		}
	}
	listen, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listen.Addr().String()
	listen.Close()
	database := filepath.Join(state, "database")
	if configured := os.Getenv("COMMANDER_SESSION_E2E_DATABASE_URL"); configured != "" {
		// Every scenario has its own database. No existing application state is touched.
		database = strings.ReplaceAll(configured, "{database}", "session_e2e_"+strings.ReplaceAll(id.New(), "-", ""))
		if database == configured {
			t.Fatal("COMMANDER_SESSION_E2E_DATABASE_URL must contain {database}")
		}
	}
	password := id.New()
	bus := filepath.Join(state, "bus")
	if configured := os.Getenv("COMMANDER_SESSION_E2E_BUS_URL"); configured != "" {
		bus = configured // Caller-provided disposable broker, never an application bus.
	}
	env := append(os.Environ(), "BUS_URL="+bus, "DATABASE_URL="+database, "STORAGE_URL="+filepath.Join(state, "storage"), "API_USERNAME=e2e", "API_PASSWORD="+password, "LOG_LEVEL=info", "FIXTURE_PEER="+peer)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	t.Cleanup(client.CloseIdleConnections)
	s := &sessionStack{processes: map[int]*os.Process{}, t: t, url: "http://" + address, password: password, client: client, env: env, binaries: binaries, peer: peer, controllerArgs: []string{"--heartbeat-timeout=6s"}}
	startE2E(t, binaries["api"], []string{"--listen-address=" + address}, env)
	s.stopController = startE2E(t, binaries["controller"], s.controllerArgs, env)
	waitUntil(t, 10*time.Second, func() bool {
		r, err := client.Get(s.url + "/readyz")
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	})
	s.waitController()
	return s
}
func (s *sessionStack) waitController() {
	waitUntil(s.t, 10*time.Second, func() bool {
		var catalog struct {
			Items []struct {
				Status string
				Stale  bool
			}
		}
		s.request("GET", "/api/v1/controllers", nil, 200, &catalog)
		for _, controller := range catalog.Items {
			if controller.Status == "ready" && !controller.Stale {
				return true
			}
		}
		return false
	})
}
func (s *sessionStack) request(method, path string, body any, want int, out any) {
	s.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		s.t.Fatal(err)
	}
	req, err := http.NewRequest(method, s.url+path, bytes.NewReader(raw))
	if err != nil {
		s.t.Fatal(err)
	}
	req.SetBasicAuth("e2e", s.password)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	if response.StatusCode != want {
		s.t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			s.t.Fatalf("decode %s: %v %s", path, err, data)
		}
	}
}
func (s *sessionStack) worker(suffix int, extra ...string) func() {
	owner := fmt.Sprintf("fixture-agent-%08x", suffix)
	env := append(append([]string{}, s.env...), "FIXTURE_AGENT_ID="+owner, "FIXTURE_PEER="+s.peer)
	env = append(env, extra...)
	process, stop := startE2EProcess(s.t, s.binaries["worker"], nil, env)
	s.processes[suffix] = process
	s.t.Cleanup(func() { _ = process.Signal(syscall.SIGCONT) })
	waitUntil(s.t, 10*time.Second, func() bool {
		var catalog struct{ Items []struct{ ID, Status string } }
		s.request("GET", "/api/v1/agents", nil, 200, &catalog)
		for _, a := range catalog.Items {
			if a.ID == owner && a.Status == "ready" {
				return true
			}
		}
		return false
	})
	return stop
}
func (s *sessionStack) mission(name string, group bool, required ...string) {
	rules := map[string]any{}
	if group {
		rules["same-agent"] = map[string]any{"for_actions": []string{"connect-resource", "use-resource", "mark-owner"}}
	}
	s.request("POST", "/api/v1/missions", map[string]any{"name": name, "objectives": []string{"Exercise durable runtime sessions"}, "required_actions": required, "starting_actions": []string{"connect-resource"}, "rules": rules, "input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"}}, 201, nil)
}
func (s *sessionStack) start(mission string) string {
	var op operations.Operation
	s.request("POST", "/api/v1/operations", map[string]any{"mission_name": mission, "input": map[string]any{}}, 201, &op)
	return op.ID
}
func (s *sessionStack) assign(op, action string, input any) operations.Task {
	var task operations.Task
	s.request("POST", "/api/v1/operations/"+op+"/tasks", map[string]any{"action_name": action, "input": input}, 201, &task)
	return task
}
func (s *sessionStack) operation(op string) operations.Operation {
	var result operations.Operation
	s.request("GET", "/api/v1/operations/"+op, nil, 200, &result)
	return result
}
func (s *sessionStack) task(op, task string) operations.Task {
	var result operations.Task
	s.request("GET", "/api/v1/operations/"+op+"/tasks/"+task, nil, 200, &result)
	return result
}
func (s *sessionStack) finished(op, task string) operations.Task {
	s.t.Helper()
	var result operations.Task
	waitUntil(s.t, 15*time.Second, func() bool {
		result = s.task(op, task)
		if result.Status == operations.TaskFailed || result.Status == operations.TaskInterrupted {
			s.t.Fatalf("task failed: %+v", result)
		}
		return result.Status == operations.TaskSucceeded
	})
	return result
}
func (s *sessionStack) list(scope string) []sessions.Session {
	var result struct{ Items []sessions.Session }
	s.request("GET", "/api/v1/sessions?scope_id="+scope, nil, 200, &result)
	return result.Items
}
func (s *sessionStack) control(ref sessions.Reference, method string) sessions.Session {
	var result sessions.Session
	s.request("POST", "/api/v1/sessions/control", map[string]any{"id": ref.ID, "scope_id": ref.ScopeID, "owner_agent_id": ref.OwnerAgentID, "method": method, "request_id": id.New()}, 200, &result)
	return result
}
func (s *sessionStack) restart() {
	s.stopController()
	s.stopController = startE2E(s.t, s.binaries["controller"], s.controllerArgs, s.env)
	s.waitController()
}
func (s *sessionStack) create(op, scope string) sessions.Session {
	s.t.Helper()
	task := s.assign(op, "connect-resource", map[string]string{"session_scope": scope})
	task = s.finished(op, task.ID)
	if task.Result == nil || task.Result.Session == nil {
		s.t.Fatalf("missing session result: %+v", task)
	}
	var output struct {
		Immutable bool `json:"checkpoint_immutable"`
	}
	if err := json.Unmarshal(task.Result.Output, &output); err != nil || !output.Immutable {
		s.t.Fatal("creation did not preserve immutable checkpoints")
	}
	ref := *task.Result.Session
	waitUntil(s.t, 5*time.Second, func() bool {
		for _, item := range s.list(scope) {
			if item.ID == ref.ID && item.State == "connected" {
				return true
			}
		}
		return false
	})
	return ref
}
func immediateSessionPeer(t *testing.T) *httptest.Server {
	t.Helper()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	t.Cleanup(peer.Close)
	return peer
}

func TestE2ESessionLifecycleOwnership(t *testing.T) {
	peer := immediateSessionPeer(t)
	s := newSessionStack(t, peer.URL)
	s.worker(1)
	s.mission("lifecycle", true, "connect-resource")
	op1 := s.start("lifecycle")
	scope := id.New()
	first := s.create(op1, scope)
	// Completed creation work does not own the resource's lifetime.
	s.request("POST", "/api/v1/operations/"+op1+"/complete", map[string]any{"output": map[string]any{}}, 200, nil)
	s.worker(2)
	op2 := s.start("lifecycle")
	use := s.assign(op2, "use-resource", map[string]string{"session_scope": scope, "session_id": first.ID})
	used := s.finished(op2, use.ID)
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
	if err := s.processes[1].Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 12*time.Second, func() bool {
		for _, v := range s.list(scope) {
			if v.ID == first.ID {
				return v.State == "lost"
			}
		}
		return false
	})
	abandoned := s.control(first.Reference, "abandon")
	if abandoned.ReleaseReason != "abandoned" || abandoned.State != "lost" {
		t.Fatalf("abandonment claimed closure: %+v", abandoned)
	}
	op3 := s.start("lifecycle")
	replacement := s.create(op3, scope)
	if replacement.ID == first.ID || replacement.OwnerAgentID == first.OwnerAgentID {
		t.Fatalf("replacement reused the old identity: %+v", replacement)
	}
	waitUntil(t, 5*time.Second, func() bool {
		op := s.operation(op2)
		for _, r := range op.Preflight.Actions {
			if len(r.ReadyAgentIDs) > 0 {
				return false
			}
		}
		return true
	})
	blocked := s.assign(op2, "use-resource", map[string]string{"session_scope": scope, "session_id": replacement.ID})
	waitUntil(t, 5*time.Second, func() bool {
		op := s.operation(op2)
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
	if task := s.task(op2, blocked.ID); task.AgentID != "" || task.Status != operations.TaskQueued {
		t.Fatalf("abandonment rebound old affinity: %+v", task)
	}
	s.restart()
	items := s.list(scope)
	if len(items) != 2 {
		t.Fatalf("history lost after restart: %+v", items)
	}
	for _, item := range items {
		if item.ID == first.ID && item.ReleaseReason != "abandoned" {
			t.Fatalf("old identity reclaimed scope: %+v", item)
		}
	}
	if err := s.processes[1].Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 8*time.Second, func() bool {
		var catalog struct {
			Items []struct {
				ID       string
				Sessions []sessions.Session
			}
		}
		s.request("GET", "/api/v1/agents", nil, 200, &catalog)
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
	for _, item := range s.list(scope) {
		if item.ID == first.ID && (item.ReleaseReason != "abandoned" || item.State != "lost") {
			t.Fatalf("late owner reclaimed abandoned scope: %+v", item)
		}
	}
	closed := s.control(replacement.Reference, "close")
	if closed.ReleaseReason != "closed" {
		t.Fatalf("close was not confirmed durably: %+v", closed)
	}
	s.restart()
	op4 := s.start("lifecycle")
	fresh := s.create(op4, scope)
	if fresh.ID == replacement.ID {
		t.Fatal("closed session identity reused")
	}
	otherScope := id.New()
	other := s.create(s.start("lifecycle"), otherScope)
	var discovered struct{ Items []sessions.Session }
	s.request("GET", "/api/v1/sessions", nil, 200, &discovered)
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
		s.request("GET", "/api/v1/sessions/"+item.ID, nil, 200, &detail)
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
		s.request("GET", "/api/v1/sessions?"+sessionFilter(filter.scope, filter.owner), nil, 200, &filtered)
		var got []string
		for _, item := range filtered.Items {
			got = append(got, item.ID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") || filtered.Items == nil {
			t.Fatalf("session filters did not intersect: filter=%+v got=%v want=%v", filter, got, want)
		}
	}
}

func sessionFilter(scope, owner string) string {
	var values []string
	if scope != "" {
		values = append(values, "scope_id="+scope)
	}
	if owner != "" {
		values = append(values, "owner_agent_id="+owner)
	}
	return strings.Join(values, "&")
}

func TestE2ESessionAffinityLookaheadAndOwnerReadiness(t *testing.T) {
	peer := immediateSessionPeer(t)
	s := newSessionStack(t, peer.URL)
	s.worker(1, "FIXTURE_USE_REQUIRES_SECRET=true")
	s.mission("create-only", false, "connect-resource")
	scope := id.New()
	resource := s.create(s.start("create-only"), scope)
	s.worker(2)
	s.mission("lookahead", true, "connect-resource")
	op := s.start("lookahead")
	parent := s.assign(op, "propose-affinity", map[string]string{"session_scope": scope, "session_id": resource.ID})
	s.finished(op, parent.ID)
	// B advertises a ready use action, but only A owns the selected session.
	waitUntil(t, 5*time.Second, func() bool {
		op := s.operation(op)
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
	current := s.operation(op)
	for _, task := range current.Tasks {
		if task.ActionName == "mark-owner" && task.AgentID != "" {
			t.Fatalf("stateless task bound group before owner became ready: %+v", task)
		}
	}
	// Public capability metadata includes mode even for typed executors.
	var agents struct {
		Items []struct {
			Actions []struct {
				Name string `json:"name"`
				Mode string `json:"session_mode"`
			}
		}
	}
	s.request("GET", "/api/v1/agents", nil, 200, &agents)
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
	s := newSessionStack(t, peer.URL)
	s.worker(1)
	s.mission("close-prepared", false, "connect-resource")
	op := s.start("close-prepared")
	scope := id.New()
	task := s.assign(op, "connect-resource", map[string]string{"session_scope": scope})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("creation never started")
	}
	var prepared sessions.Session
	waitUntil(t, 5*time.Second, func() bool {
		items := s.list(scope)
		if len(items) == 1 {
			prepared = items[0]
			return prepared.OwnerAgentID != ""
		}
		return false
	})
	closed := s.control(prepared.Reference, "close")
	if closed.State != "closed" || closed.ReleaseReason != "closed" {
		t.Fatalf("missing closure receipt: %+v", closed)
	}
	once.Do(func() { close(release) })
	waitUntil(t, 5*time.Second, func() bool { return s.task(op, task.ID).Status == operations.TaskFailed })
	s.restart()
	items := s.list(scope)
	if len(items) != 1 || items[0].ReleaseReason != "closed" {
		t.Fatalf("late creation observation reopened closed claim: %+v", items)
	}
	replacement := s.create(s.start("close-prepared"), scope)
	if replacement.ID == prepared.ID {
		t.Fatal("replacement inherited closed identity")
	}
}

func TestE2ESessionCancellationReleasesUnstartedScope(t *testing.T) {
	peer := immediateSessionPeer(t)
	s := newSessionStack(t, peer.URL)
	s.worker(1)
	s.mission("blocked-create", false, "connect-resource", "missing-capability")
	s.mission("available-create", false, "connect-resource")
	for _, method := range []string{"cancel", "fail"} {
		t.Run(method, func(t *testing.T) {
			scope := id.New()
			op := s.start("blocked-create")
			task := s.assign(op, "connect-resource", map[string]string{"session_scope": scope})
			current := s.task(op, task.ID)
			if current.Status != operations.TaskQueued || current.AgentID != "" {
				t.Fatalf("expected undispatched reservation: %+v", current)
			}
			payload := map[string]any{}
			if method == "fail" {
				payload["reason"] = "fixture stopping queued work"
			}
			s.request("POST", "/api/v1/operations/"+op+"/"+method, payload, 200, nil)
			items := s.list(scope)
			if len(items) != 1 || items[0].ReleaseReason != "cancelled_before_effect" {
				t.Fatalf("unstarted reservation retained: %+v", items)
			}
			replacement := s.create(s.start("available-create"), scope)
			if replacement.ID == task.ID {
				t.Fatal("cancelled identity reused")
			}
		})
	}
}

func TestE2ESessionGroupLookaheadAndIndependentOwners(t *testing.T) {
	peer := immediateSessionPeer(t)
	s := newSessionStack(t, peer.URL)
	s.worker(1)
	s.mission("ungrouped", false, "connect-resource")
	firstScope := id.New()
	first := s.create(s.start("ungrouped"), firstScope)
	s.worker(2, "FIXTURE_NO_PROPOSER=true")
	secondScope := id.New()
	second := s.create(s.start("ungrouped"), secondScope)
	if first.OwnerAgentID == second.OwnerAgentID {
		t.Fatal("fixture needs two different resource owners")
	}
	s.mission("grouped", true, "connect-resource")
	op := s.start("grouped")
	parent := s.assign(op, "propose-affinity", map[string]string{"session_scope": firstScope, "session_id": first.ID})
	s.finished(op, parent.ID)
	waitUntil(t, 10*time.Second, func() bool {
		current := s.operation(op)
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
	separate := s.start("ungrouped")
	a := s.assign(separate, "use-resource", map[string]string{"session_scope": firstScope, "session_id": first.ID})
	b := s.assign(separate, "use-resource", map[string]string{"session_scope": secondScope, "session_id": second.ID})
	if s.finished(separate, a.ID).AgentID != first.OwnerAgentID || s.finished(separate, b.ID).AgentID != second.OwnerAgentID {
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
	s := newSessionStack(t, peer.URL)
	s.worker(1, "FIXTURE_SESSION_STATE=listening")
	s.mission("reuse", false, "connect-resource")
	op, scope := s.start("reuse"), id.New()
	created := s.assign(op, "connect-resource", map[string]string{"session_scope": scope})
	result := s.finished(op, created.ID)
	ref := result.Result.Session.Reference
	waitUntil(t, 5*time.Second, func() bool {
		items := s.list(scope)
		return len(items) == 1 && items[0].State == "listening"
	})
	s.worker(2)
	op2 := s.start("reuse")
	input := map[string]string{"session_scope": scope, "session_id": ref.ID}
	use := s.assign(op2, "use-resource", input)
	waitUntil(t, 5*time.Second, func() bool {
		for _, task := range s.operation(op2).Preflight.Tasks {
			if task.TaskID == use.ID && strings.Contains(task.Reason, "session_not_usable") {
				return true
			}
		}
		return false
	})
	reuse := s.assign(op2, "connect-resource", input)
	reused := s.finished(op2, reuse.ID)
	if reused.SessionIntent.Mode != sessions.Reuse || reused.AgentID != ref.OwnerAgentID || reused.Result.Session.Reference != ref || creations.Load() != 1 {
		t.Fatalf("explicit reuse opened a replacement: %+v", reused)
	}
	s.control(ref, "verify")
	if s.finished(op2, use.ID).AgentID != ref.OwnerAgentID {
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
	s := newSessionStack(t, peer.URL)
	s.worker(1)
	s.mission("close-recovery", false, "connect-resource")
	scope := id.New()
	first := s.create(s.start("close-recovery"), scope)
	request := map[string]any{"id": first.ID, "scope_id": scope, "owner_agent_id": first.OwnerAgentID, "method": "close", "request_id": id.New()}
	s.request("POST", "/api/v1/sessions/control", request, 422, nil)
	items := s.list(scope)
	if len(items) != 1 || items[0].Disposition != "closing" || items[0].ReleaseReason != "" {
		t.Fatalf("failed close released the scope: %+v", items)
	}
	op := s.start("close-recovery")
	s.request("POST", "/api/v1/operations/"+op+"/tasks", map[string]any{"action_name": "connect-resource", "input": map[string]string{"session_scope": scope}}, 422, nil)
	s.restart()
	allowClose.Store(true)
	waitUntil(t, 10*time.Second, func() bool {
		items := s.list(scope)
		return len(items) == 1 && items[0].ReleaseReason == "closed"
	})
	if replacement := s.create(op, scope); replacement.ID == first.ID {
		t.Fatal("confirmed replacement reused the old reference")
	}
}

func TestE2ESessionIntentBeforeRegistration(t *testing.T) {
	peer := immediateSessionPeer(t)
	s := newSessionStack(t, peer.URL)
	contract := map[string]any{"name": "connect-resource", "session_mode": "create", "input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"}}
	s.request("POST", "/api/v1/missions", map[string]any{"name": "before-registration", "objectives": []string{"Accept a session contract before its executor arrives"}, "required_actions": []string{"connect-resource"}, "starting_actions": []string{"connect-resource"}, "action_definitions": []any{contract}, "input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"}}, 201, nil)
	op, scope := s.start("before-registration"), id.New()
	task := s.assign(op, "connect-resource", map[string]string{"session_scope": scope})
	items := s.list(scope)
	if len(items) != 1 || items[0].ID != task.ID || items[0].OwnerAgentID != "" || items[0].Disposition != "reserved" {
		t.Fatalf("missing unowned durable admission: %+v", items)
	}
	s.restart()
	s.worker(1)
	finished := s.finished(op, task.ID)
	if finished.SessionIntent.Mode != sessions.Create || finished.Result.Session.ID != task.ID {
		t.Fatalf("registration reinterpreted accepted session intent: %+v", finished)
	}
}

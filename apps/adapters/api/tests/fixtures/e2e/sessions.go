package e2e

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
	"syscall"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

func WaitUntil(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("public state did not become ready")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func StartE2E(t *testing.T, binary string, args, env []string) func() {
	_, stop := startE2EProcess(t, binary, args, env)
	return stop
}

func startE2EProcess(t *testing.T, binary string, args, env []string) (*os.Process, func()) {
	t.Helper()
	log, err := os.CreateTemp(t.TempDir(), "process-*.log")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, args...)
	command.Env, command.Stdout, command.Stderr = env, log, log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { command.Wait(); close(done) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			command.Process.Signal(os.Interrupt)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				command.Process.Kill()
				<-done
			}
			log.Close()
		})
	}
	t.Cleanup(func() {
		stop()
		if t.Failed() {
			data, _ := os.ReadFile(log.Name())
			t.Logf("%s: %s", filepath.Base(binary), data)
		}
	})
	return command.Process, stop
}

func RequireFilesystemFixture(t *testing.T) {
	t.Helper()
	if os.Getenv("COMMANDER_SESSION_E2E_DATABASE_URL") != "" || os.Getenv("COMMANDER_SESSION_E2E_BUS_URL") != "" {
		t.Skip("fault controller fixture uses filesystem providers")
	}
}

type SessionStack struct {
	processes           map[int]*os.Process
	t                   *testing.T
	url, password       string
	client              *http.Client
	env, controllerArgs []string
	binaries            map[string]string
	stopController      func()
	peer                string
}

func (s *SessionStack) WithTest(t *testing.T) func() {
	previous := s.t
	s.t = t
	return func() { s.t = previous }
}

func (s *SessionStack) SignalWorker(suffix int, signal os.Signal) error {
	return s.processes[suffix].Signal(signal)
}

func (s *SessionStack) StopController() { s.stopController() }

func NewSessionStack(t *testing.T, peer string, controllerSource ...string) *SessionStack {
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
	s := &SessionStack{processes: map[int]*os.Process{}, t: t, url: "http://" + address, password: password, client: client, env: env, binaries: binaries, peer: peer, controllerArgs: []string{"--heartbeat-timeout=6s"}}
	StartE2E(t, binaries["api"], []string{"--listen-address=" + address}, env)
	s.stopController = StartE2E(t, binaries["controller"], s.controllerArgs, env)
	WaitUntil(t, 10*time.Second, func() bool {
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

func (s *SessionStack) waitController() {
	WaitUntil(s.t, 10*time.Second, func() bool {
		var catalog struct {
			Items []struct {
				Status string
				Stale  bool
			}
		}
		s.Request("GET", "/api/v1/controllers", nil, 200, &catalog)
		for _, controller := range catalog.Items {
			if controller.Status == "ready" && !controller.Stale {
				return true
			}
		}
		return false
	})
}

func (s *SessionStack) Request(method, path string, body any, want int, out any) {
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

func (s *SessionStack) Worker(suffix int, extra ...string) func() {
	owner := fmt.Sprintf("fixture-agent-%08x", suffix)
	env := append(append([]string{}, s.env...), "FIXTURE_AGENT_ID="+owner, "FIXTURE_PEER="+s.peer)
	env = append(env, extra...)
	process, stop := startE2EProcess(s.t, s.binaries["worker"], nil, env)
	s.processes[suffix] = process
	s.t.Cleanup(func() { _ = process.Signal(syscall.SIGCONT) })
	WaitUntil(s.t, 10*time.Second, func() bool {
		var catalog struct{ Items []struct{ ID, Status string } }
		s.Request("GET", "/api/v1/agents", nil, 200, &catalog)
		for _, a := range catalog.Items {
			if a.ID == owner && a.Status == "ready" {
				return true
			}
		}
		return false
	})
	return stop
}

func (s *SessionStack) Mission(name string, group bool, required ...string) {
	rules := map[string]any{}
	if group {
		rules["same-agent"] = map[string]any{"for_actions": []string{"connect-resource", "use-resource", "mark-owner"}}
	}
	s.Request("POST", "/api/v1/missions", map[string]any{"name": name, "objectives": []string{"Exercise durable runtime sessions"}, "required_actions": required, "starting_actions": []string{"connect-resource"}, "rules": rules, "input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"}}, 201, nil)
}

func (s *SessionStack) Start(mission string) string {
	var op operations.Operation
	s.Request("POST", "/api/v1/operations", map[string]any{"mission_name": mission, "input": map[string]any{}}, 201, &op)
	return op.ID
}

func (s *SessionStack) Assign(op, action string, input any) operations.Task {
	var task operations.Task
	s.Request("POST", "/api/v1/operations/"+op+"/tasks", map[string]any{"action_name": action, "input": input}, 201, &task)
	return task
}

func (s *SessionStack) Operation(op string) operations.Operation {
	var result operations.Operation
	s.Request("GET", "/api/v1/operations/"+op, nil, 200, &result)
	return result
}

func (s *SessionStack) Task(op, task string) operations.Task {
	var result operations.Task
	s.Request("GET", "/api/v1/operations/"+op+"/tasks/"+task, nil, 200, &result)
	return result
}

func (s *SessionStack) Finished(op, task string) operations.Task {
	s.t.Helper()
	var result operations.Task
	WaitUntil(s.t, 15*time.Second, func() bool {
		result = s.Task(op, task)
		if result.Status == operations.TaskFailed || result.Status == operations.TaskInterrupted {
			s.t.Fatalf("task failed: %+v", result)
		}
		return result.Status == operations.TaskSucceeded
	})
	return result
}

func (s *SessionStack) List(scope string) []sessions.Session {
	var result struct{ Items []sessions.Session }
	s.Request("GET", "/api/v1/sessions?scope_id="+scope, nil, 200, &result)
	return result.Items
}

func (s *SessionStack) Control(ref sessions.Reference, method string) sessions.Session {
	var result sessions.Session
	s.Request("POST", "/api/v1/sessions/control", map[string]any{"id": ref.ID, "scope_id": ref.ScopeID, "owner_agent_id": ref.OwnerAgentID, "method": method, "request_id": id.New()}, 200, &result)
	return result
}

func (s *SessionStack) Restart() {
	s.stopController()
	s.stopController = StartE2E(s.t, s.binaries["controller"], s.controllerArgs, s.env)
	s.waitController()
}

func ImmediateSessionPeer(t *testing.T) *httptest.Server {
	t.Helper()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	t.Cleanup(peer.Close)
	return peer
}

func SessionFilter(scope, owner string) string {
	var values []string
	if scope != "" {
		values = append(values, "scope_id="+scope)
	}
	if owner != "" {
		values = append(values, "owner_agent_id="+owner)
	}
	return strings.Join(values, "&")
}

type sessionMCPClient struct {
	t        *testing.T
	url      string
	token    string
	client   *http.Client
	sequence int
}

func (s *SessionStack) MCP() *sessionMCPClient {
	s.t.Helper()
	root, err := filepath.Abs("../../../../..")
	if err != nil {
		s.t.Fatal(err)
	}
	binary := filepath.Join(s.t.TempDir(), "mcp")
	build := exec.Command("go", "build", "-o", binary, "./apps/adapters/mcp/cmd/commander-mcp")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		s.t.Fatalf("build mcp: %v\n%s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	token := id.New()
	env := append(append([]string{}, s.env...), "MCP_TOKEN="+token)
	StartE2E(s.t, binary, []string{"--listen-address=" + address}, env)
	client := &sessionMCPClient{t: s.t, url: "http://" + address, token: token, client: s.client}
	WaitUntil(s.t, 10*time.Second, func() bool {
		response, err := s.client.Get(client.url + "/healthz")
		if err != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == http.StatusOK
	})
	client.RPC("initialize", map[string]any{
		"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "session-e2e", "version": "1"},
	}, 0)
	return client
}

func (c *sessionMCPClient) RPC(method string, params any, wantError int) json.RawMessage {
	c.t.Helper()
	c.sequence++
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.sequence, "method": method, "params": params})
	if err != nil {
		c.t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(c.t.Context(), "POST", c.url+"/mcp", bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	response, err := c.client.Do(request)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		c.t.Fatalf("MCP %s: HTTP %d", method, response.StatusCode)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		c.t.Fatal(err)
	}
	if envelope.Error.Code != wantError {
		c.t.Fatalf("MCP %s: error %+v, want code %d", method, envelope.Error, wantError)
	}
	return envelope.Result
}

func (c *sessionMCPClient) Call(name string, arguments, out any) {
	c.t.Helper()
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(c.RPC("tools/call", map[string]any{"name": name, "arguments": arguments}, 0), &result); err != nil {
		c.t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" {
		c.t.Fatalf("MCP %s: %+v", name, result)
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), out); err != nil {
		c.t.Fatal(err)
	}
}

func (c *sessionMCPClient) List(arguments map[string]any) []sessions.Session {
	c.t.Helper()
	var result struct {
		Sessions []sessions.Session `json:"sessions"`
	}
	c.Call("list_runtime_sessions", arguments, &result)
	if result.Sessions == nil {
		c.t.Fatal("session listing must return an array, not null")
	}
	return result.Sessions
}

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"commander/framework/id"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var mcpBinary, agentBinary, controllerBinary string

// Build each executable once; every scenario runs real, independent processes.
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	binaries, err := os.MkdirTemp("", "commander-mcp-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := func() int {
		defer os.RemoveAll(binaries)
		root, err := filepath.Abs("../../../..")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, build := range []struct {
			name, directory, pkg string
			target               *string
		}{
			{"commander-mcp", filepath.Join(root, "apps", "adapters", "mcp"), "./cmd/commander-mcp", &mcpBinary},
			{"commander-web-quality-agent", root, "./apps/agents/web-quality/cmd", &agentBinary},
			{"commander-controller", root, "./apps/controllers/default/cmd", &controllerBinary},
		} {
			*build.target = filepath.Join(binaries, build.name)
			if runtime.GOOS == "windows" {
				*build.target += ".exe"
			}
			command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-race", "-buildvcs=false", "-o", *build.target, build.pkg)
			command.Dir = build.directory
			if output, err := command.CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "build %s: %v\n%s", build.name, err, output)
				return 1
			}
		}
		return m.Run()
	}()
	os.Exit(code)
}

type deployment struct {
	ctx               context.Context
	root              string
	env               map[string]string
	controllerProcess *process
	controllerArgs    []string
}

func newDeployment(t *testing.T) *deployment {
	t.Helper()
	if testing.Short() {
		t.Skip("process end-to-end test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	connection := func(name string) string {
		path := filepath.Join(root, name)
		if runtime.GOOS == "windows" {
			return path
		}
		return (&url.URL{Scheme: "file", Path: path}).String()
	}
	return &deployment{ctx: ctx, root: root, env: map[string]string{
		"BUS_URL": connection("bus"), "STORAGE_URL": connection("storage"), "DATABASE_URL": connection("database"),
		"CONTROLLER_TYPE": "default",
		"CONTROLLER_ID":   "", "ADAPTER_ID": "", "AGENT_ID": "",
		"LOG_LEVEL": "info", "LOG_FORMAT": "json",
		"SERVER_IP_ADDRESS": "", "SERVER_HOSTNAME": "", "CHROMIUM_PATH": "",
	}}
}

func processEnvironment(values map[string]string) []string {
	var result []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[key]; !replaced {
			result = append(result, entry)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

type process struct {
	command  *exec.Cmd
	exited   chan struct{}
	err      error // Published when exited closes.
	logPath  string
	id       string
	typeName string
}

func (d *deployment) start(t *testing.T, binary string, env map[string]string, args ...string) *process {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "process.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	command := exec.CommandContext(d.ctx, binary, args...)
	command.Dir, command.Env = d.root, processEnvironment(env)
	command.Stdout, command.Stderr, command.WaitDelay = log, log, 3*time.Second
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	p := &process{command: command, exited: make(chan struct{}), logPath: logPath}
	go func() { p.err = command.Wait(); close(p.exited) }()
	t.Cleanup(func() {
		if !p.stopped() {
			_ = command.Process.Kill()
		}
		select {
		case <-p.exited:
		case <-time.After(5 * time.Second):
			t.Error("child process cleanup timed out")
		}
		if t.Failed() {
			data, _ := os.ReadFile(logPath)
			t.Logf("%s logs:\n%s", filepath.Base(binary), data)
		}
	})
	return p
}

func (p *process) stopped() bool {
	select {
	case <-p.exited:
		return true
	default:
		return false
	}
}

func (p *process) wait(t *testing.T) {
	t.Helper()
	select {
	case <-p.exited:
		if p.err != nil {
			t.Fatalf("process failed: %v", p.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("process did not exit cleanly")
	}
}

func (p *process) interrupt(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("graceful process signals require Unix")
	}
	if err := p.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	p.wait(t)
}

// A crash test must distinguish the intended SIGKILL from a process that had
// already failed on its own; graceful shutdown checks keep using wait.
func (p *process) kill(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("crash signal verification requires Unix")
	}
	if err := p.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.exited:
		var exit *exec.ExitError
		if !errors.As(p.err, &exit) {
			t.Fatalf("process did not exit from SIGKILL: %v", p.err)
		}
		status, ok := exit.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
			t.Fatalf("unexpected crash exit: %v", p.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("killed process did not exit")
	}
}

func (d *deployment) agent(t *testing.T) *process {
	t.Helper()
	return d.start(t, agentBinary, d.env, "--actions=check-links", "--permissions=network-access", "--inline-limit=0", "--heartbeat-interval=100ms")
}

func (d *deployment) controller(t *testing.T, args ...string) *process {
	t.Helper()
	if d.controllerProcess != nil && !d.controllerProcess.stopped() {
		return d.controllerProcess
	}
	if len(args) != 0 {
		d.controllerArgs = append([]string(nil), args...)
	}
	previous := d.controllerProcess
	p := d.start(t, controllerBinary, d.env, d.controllerArgs...)
	d.controllerProcess = p
	poll(t, func() bool {
		data, _ := os.ReadFile(p.logPath)
		if p.stopped() {
			t.Fatalf("controller startup failed: %v\n%s", p.err, data)
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			var entry struct {
				Message string `json:"msg"`
				ID      string `json:"controller_id"`
				Type    string `json:"controller_type"`
			}
			if json.Unmarshal(line, &entry) == nil && entry.Message == "Controller ready" {
				p.id, p.typeName = entry.ID, entry.Type
				d.assertHostLog(t, line, "commander-controller", "controller", p.id, p.typeName)
				if runtimeType, valid := id.RuntimeType(p.id, id.Controller); !valid || runtimeType != "default" || runtimeType != p.typeName {
					t.Fatalf("unexpected controller identity: id=%q type=%q", p.id, p.typeName)
				}
				if previous != nil && p.id == previous.id {
					t.Fatalf("controller restart reused runtime ID %q", p.id)
				}
				return true
			}
		}
		return false
	})
	return p
}

func (d *deployment) mcp(t *testing.T, override bool, args ...string) (string, *process) {
	t.Helper()
	d.controller(t)
	return d.mcpOnly(t, override, args...)
}

// Starting MCP alone proves queries do not require a running controller.
func (d *deployment) mcpOnly(t *testing.T, override bool, args ...string) (string, *process) {
	t.Helper()
	env := map[string]string{
		"BUS_URL": d.env["BUS_URL"], "LOG_LEVEL": d.env["LOG_LEVEL"], "LOG_FORMAT": d.env["LOG_FORMAT"],
		"CONTROLLER_TYPE": d.env["CONTROLLER_TYPE"], "ADAPTER_ID": d.env["ADAPTER_ID"],
		"STORAGE_URL": d.env["STORAGE_URL"], "DATABASE_URL": d.env["DATABASE_URL"],
	}
	if override {
		env["BUS_URL"] = "unsupported://must-not-be-used"
		env["STORAGE_URL"], env["DATABASE_URL"] = "unsupported://must-not-be-used", "unsupported://must-not-be-used"
		env["LOG_LEVEL"], env["LOG_FORMAT"] = "invalid-environment-value", "invalid-environment-value"
		args = append(args, "--bus-url="+d.env["BUS_URL"], "--storage-url="+d.env["STORAGE_URL"], "--database-url="+d.env["DATABASE_URL"], "--log-level=info", "--log-format=json")
	}
	p := d.start(t, mcpBinary, env, append([]string{"--listen-address=127.0.0.1:0"}, args...)...)
	return d.readyHTTP(t, p) + "/mcp", p
}

func (d *deployment) readyHTTP(t *testing.T, p *process) string {
	t.Helper()
	var endpoint string
	poll(t, func() bool {
		data, _ := os.ReadFile(p.logPath)
		if p.stopped() {
			t.Fatalf("%s startup failed: %v\n%s", filepath.Base(p.command.Path), p.err, data)
		}
		// Port zero is selected by the OS and reported in structured startup logs.
		for _, line := range bytes.Split(data, []byte("\n")) {
			var entry struct {
				Address string
				ID      string `json:"adapter_id"`
				Type    string `json:"adapter_type"`
			}
			if json.Unmarshal(line, &entry) == nil && entry.Address != "" {
				endpoint = "http://" + entry.Address
				p.id, p.typeName = entry.ID, entry.Type
				d.assertHostLog(t, line, "commander-mcp", "adapter", p.id, p.typeName)
				if runtimeType, valid := id.RuntimeType(p.id, id.Adapter); !valid || runtimeType != "mcp" || p.typeName != runtimeType {
					t.Fatalf("unexpected adapter identity: id=%q type=%q", p.id, p.typeName)
				}
				break
			}
		}
		if endpoint == "" {
			return false
		}
		request, _ := http.NewRequestWithContext(d.ctx, http.MethodGet, endpoint+"/healthz", nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		var health struct{ Status string }
		return response.StatusCode == http.StatusOK && json.NewDecoder(response.Body).Decode(&health) == nil && health.Status == "ok"
	})
	return endpoint
}

func (d *deployment) assertHostLog(t *testing.T, line []byte, service, role, runtimeID, runtimeType string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatalf("invalid structured host log: %v: %s", err, line)
	}
	for key, want := range map[string]string{"service": service, role + "_id": runtimeID, role + "_type": runtimeType} {
		if record[key] != want {
			t.Errorf("log %s = %v, want %q: %s", key, record[key], want, line)
		}
	}
	for _, key := range []string{"component", "bus_url", "storage_url", "database_url"} {
		if _, present := record[key]; present {
			t.Errorf("log contains removed field %q: %s", key, line)
		}
	}
	for _, key := range []string{"BUS_URL", "STORAGE_URL", "DATABASE_URL"} {
		if value := d.env[key]; value != "" && bytes.Contains(line, []byte(value)) {
			t.Errorf("log exposes configured %s: %s", key, line)
		}
	}
	for _, other := range []string{"agent", "adapter", "controller"} {
		if _, present := record[other+"_type"]; other != role && present {
			t.Errorf("log has another host's type field: %s", line)
		}
	}
}

func poll(t *testing.T, ready func() bool) {
	t.Helper()
	timer, ticker := time.NewTimer(10*time.Second), time.NewTicker(25*time.Millisecond)
	defer timer.Stop()
	defer ticker.Stop()
	for !ready() {
		select {
		case <-timer.C:
			t.Fatal("public behavior did not become ready within 10 seconds")
		case <-ticker.C:
		}
	}
}

func connect(t *testing.T, ctx context.Context, endpoint string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "commander-e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatalf("connect MCP: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func call(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("%s: %+v", name, result.Content)
	}
	return result
}

func rejected(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args any) {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err == nil && !result.IsError {
		t.Fatalf("%s accepted invalid input: %+v", name, args)
	}
}

func decode[T any](t *testing.T, result *mcp.CallToolResult) T {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	var value T
	if err == nil {
		err = json.Unmarshal(data, &value)
	}
	if err != nil {
		t.Fatalf("decode public response: %v", err)
	}
	return value
}

type agentView struct {
	ID, Type, Status string
	Stale            bool
	Actions          []struct{ Name string }
}

func agents(t *testing.T, d *deployment, session *mcp.ClientSession) []agentView {
	t.Helper()
	result := call(t, d.ctx, session, "list_agents", map[string]any{})
	for _, record := range decode[struct{ Agents []map[string]json.RawMessage }](t, result).Agents {
		if _, exists := record["name"]; exists {
			t.Fatal("agent response still exposes the removed name field")
		}
	}
	return decode[struct{ Agents []agentView }](t, result).Agents
}

func readyAgents(t *testing.T, d *deployment, session *mcp.ClientSession, count int) []agentView {
	t.Helper()
	var records []agentView
	poll(t, func() bool {
		records = agents(t, d, session)
		if len(records) != count {
			return false
		}
		for _, record := range records {
			if record.Stale || record.Status != "ready" {
				return false
			}
		}
		return true
	})
	return records
}

func readLinkArtifact(t *testing.T, ctx context.Context, session *mcp.ClientSession, result *mcp.CallToolResult) string {
	t.Helper()
	for _, content := range result.Content {
		if link, ok := content.(*mcp.ResourceLink); ok {
			resource, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: link.URI})
			if err != nil || len(resource.Contents) != 1 {
				t.Fatalf("read linked artifact: %+v, %v", resource, err)
			}
			var observation struct {
				URL        string `json:"url"`
				StatusCode int    `json:"status_code"`
			}
			if err := json.Unmarshal([]byte(resource.Contents[0].Text), &observation); err != nil {
				t.Fatal(err)
			}
			if observation.StatusCode != http.StatusOK {
				t.Fatalf("HTTP observation status: %d", observation.StatusCode)
			}
			return strings.TrimSuffix(observation.URL, "/")
		}
	}
	t.Fatal("action result did not include its stored artifact link")
	return ""
}

// Keep HTTP response draining bounded; used by live protocol guard checks.
func responseBody(response *http.Response) []byte {
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return body
}

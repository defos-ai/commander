package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"commander/framework/artifact"
	"commander/framework/comms"
	"commander/framework/comms/messages"
	fsartifact "commander/providers/fs/artifact"
	fscomms "commander/providers/fs/comms"
)

// Exercise the shipped executable and its public message protocol. Building once
// keeps the suite independent of runtime constructors without paying per case.
func TestCommanderAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the agent executable")
	}
	// Arrange.
	binary := filepath.Join(t.TempDir(), "commander-web-quality-agent")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-race", "-o", binary, "./cmd")
	build.Dir = moduleRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agent: %v\n%s", err, output)
	}

	// Act and Assert: each scenario starts the real executable independently.
	t.Run("no controller dependencies", testAgentDependencies)
	t.Run("help and invalid configuration", func(t *testing.T) { testConfiguration(t, binary) })
	t.Run("action catalog", func(t *testing.T) { testActionCatalog(t, binary) })
	t.Run("environment configuration", func(t *testing.T) { testHTTPWorkflow(t, binary, false) })
	t.Run("flags override environment", func(t *testing.T) { testHTTPWorkflow(t, binary, true) })
	t.Run("generated identity", func(t *testing.T) { testGeneratedIdentity(t, binary) })
	t.Run("permission denial and recovery", func(t *testing.T) { testPermissions(t, binary) })
	t.Run("duplicate command replays saved result", func(t *testing.T) { testRepeatedActionCommand(t, binary) })
	t.Run("conflicting command does not repeat work", func(t *testing.T) { testConflictingActionCommand(t, binary) })
	t.Run("result delivery requires a matching receipt", func(t *testing.T) { testResultReceipts(t, binary) })
	t.Run("pending result retries while another action runs", func(t *testing.T) { testRetryDuringWork(t, binary) })
	t.Run("graceful stop waits for accepted results", func(t *testing.T) { testStopPendingResult(t, binary, true) })
	t.Run("graceful stop refuses unaccepted results", func(t *testing.T) { testStopPendingResult(t, binary, false) })
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run("shutdown on "+signal.String(), func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("Unix process signals")
			}
			testSignal(t, binary, signal)
		})
	}
}

func testAgentDependencies(t *testing.T) {
	// Arrange.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "list", "-deps", "./cmd")
	command.Dir = moduleRoot(t)

	// Act.
	output, err := command.CombinedOutput()

	// Assert.
	if err != nil {
		t.Fatalf("list agent dependencies: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		for _, controllerPackage := range []string{
			"commander/framework/controller",
			"commander/providers/controller",
			"commander/providers/fs/controller",
		} {
			if dependency == controllerPackage || strings.HasPrefix(dependency, controllerPackage+"/") {
				t.Errorf("agent executable imports controller package %s", dependency)
			}
		}
	}
}

func testConfiguration(t *testing.T, binary string) {
	for _, test := range []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"help ignores secret and invalid environment", []string{"--help"}, map[string]string{
			"BUS_URL": "nats://user:secret-credential@localhost", "STORAGE_URL": "s3://secret-bucket",
			"LOG_LEVEL": "secret-level", "LOG_FORMAT": "secret-format", "SERVER_IP_ADDRESS": "invalid-ip",
		}, "Usage: commander-web-quality-agent"},
		{"unsupported broker", nil, map[string]string{"BUS_URL": "nats://user:secret-credential@localhost"}, "unsupported"},
		{"unsupported storage", nil, map[string]string{"STORAGE_URL": "s3://secret-bucket"}, "unsupported"},
		{"malformed secret connection", []string{"--bus-url=amqp://user:secret-credential@localhost/%zz?token=secret-token"}, nil, "invalid"},
		{"filesystem credentials", []string{"--storage-url=file://user:secret-credential@localhost/state"}, nil, "credentials"},
		{"filesystem query secret", []string{"--storage-url=file:///state?token=secret-token"}, nil, "query"},
		{"invalid logging environment", nil, map[string]string{"LOG_LEVEL": "verbose"}, "log level"},
		{"invalid server environment", nil, map[string]string{"SERVER_IP_ADDRESS": "not-an-ip"}, "IP"},
		{"removed state directory", []string{"--state-dir=unused"}, nil, "flag provided but not defined"},
		{"removed server flag", []string{"--server-hostname=pod"}, nil, "flag provided but not defined"},
		{"removed name flag", []string{"--name=old-name"}, nil, "flag provided but not defined"},
		{"removed controller identity flag", []string{"--controller-id=old-id"}, nil, "flag provided but not defined"},
		{"removed agent identity flag", []string{"--agent-id=web-quality-agent-0123abcd"}, nil, "flag provided but not defined"},
		{"removed agent type flag", []string{"--type=web-quality"}, nil, "flag provided but not defined"},
		{"removed controller type flag", []string{"--controller-type=default"}, nil, "flag provided but not defined"},
		{"unknown action", []string{"--actions=unknown"}, nil, "unknown action"},
		{"duplicate actions", []string{"--actions=check-links,check-links"}, nil, "duplicate"},
		{"duplicate permissions", []string{"--permissions=network-access,network-access"}, nil, "duplicate"},
		{"negative heartbeat", []string{"--heartbeat-interval=-1s"}, nil, "heartbeat-interval"},
		{"explicit empty bus overrides environment", []string{"--bus-url="}, nil, "bus-url"},
		{"explicit empty logging overrides environment", []string{"--log-format="}, nil, "log format"},
		{"positional argument", []string{"unexpected"}, nil, "unexpected positional argument"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			env := configuredEnvironment(root)
			for key, value := range test.env {
				env[key] = value
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, test.args...)
			command.Dir, command.Env = root, childEnvironment(env)

			// Act.
			output, err := command.CombinedOutput()

			// Assert.
			isHelp := len(test.args) == 1 && test.args[0] == "--help"
			if isHelp && err != nil || !isHelp && err == nil {
				t.Fatalf("unexpected exit: %v\n%s", err, output)
			}
			if !isHelp {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 {
					t.Fatalf("invalid configuration did not exit with status 1: %v\n%s", err, output)
				}
			}
			if !strings.Contains(string(output), test.want) || strings.Contains(string(output), "secret-") {
				t.Fatalf("missing diagnostic or leaked environment secret: %s", output)
			}
			removedFlag := regexp.MustCompile(`(?m)^\s+-(state-dir|server-hostname|name|controller-id|controller-type|agent-id|type)(\s|$)`)
			if isHelp && (removedFlag.Match(output) || strings.Contains(string(output), "AGENT_ID") || strings.Contains(string(output), "CONTROLLER_TYPE")) {
				t.Fatalf("help advertises removed flags: %s", output)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("configuration-only command created files: %v, %v", entries, err)
			}
		})
	}
}

func testActionCatalog(t *testing.T, binary string) {
	for _, test := range []struct {
		name      string
		args      []string
		wantNames []string
	}{
		{name: "default catalog without Chromium", wantNames: []string{"capture-page", "check-accessibility", "check-links", "check-seo", "detect-technologies", "discover-pages"}},
		{name: "explicit empty selection exposes no actions", args: []string{"--actions="}},
		{name: "HTTP checks do not require Chromium", args: []string{"--actions=check-links,check-seo,detect-technologies", "--permissions=network-access"}, wantNames: []string{"check-links", "check-seo", "detect-technologies"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: dependency discovery is isolated from executables installed on the host.
			root := t.TempDir()
			env := configuredEnvironment(root)
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "google-chrome"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
				t.Fatal(err)
			}
			env["PATH"] = bin // Installed Chrome must not override the explicit empty scope.
			host := startAgent(t, binary, root, env, append(test.args, "--heartbeat-interval=0", "--search-paths="))

			// Act: discovery reports readiness without launching a browser or an HTTP request.
			started := host.receive(t, messages.AgentStarted, "")
			discover := host.send(t, messages.DiscoverActions, json.RawMessage(`{"scope":{"paths":[]}}`))
			updated := host.receive(t, messages.AgentUpdated, discover.ID)
			stop := host.send(t, messages.StopAgent, nil)
			stopped := host.receive(t, messages.AgentStopped, stop.ID)
			host.wait(t)

			// Assert: missing browser dependencies disable those actions, not the entire agent.
			for _, message := range []messages.Message{started, updated, stopped} {
				var description struct {
					Status  string `json:"status"`
					Actions []struct {
						Name                string   `json:"name"`
						Status              string   `json:"status"`
						StatusReason        string   `json:"status_reason"`
						RequiredPermissions []string `json:"required_permissions"`
					} `json:"actions"`
				}
				decode(t, message.Payload, &description)
				if message.Type != messages.AgentStopped && description.Status != "ready" {
					t.Fatalf("missing browser stopped the agent: %s", message.Payload)
				}
				var names []string
				for _, selected := range description.Actions {
					names = append(names, selected.Name)
					browser := selected.Name == "capture-page" || selected.Name == "check-accessibility"
					wantStatus := "ready"
					if browser {
						wantStatus = "unavailable"
						if !strings.Contains(selected.StatusReason, "Chromium") {
							t.Errorf("unavailable browser action did not explain its missing dependency: %+v", selected)
						}
					}
					if selected.Status != wantStatus {
						t.Errorf("%s: %s status=%q, want %q", message.Type, selected.Name, selected.Status, wantStatus)
					}
					if !browser && !slices.Equal(selected.RequiredPermissions, []string{"network-access"}) {
						t.Errorf("%s: HTTP action has unexpected permissions: %+v", message.Type, selected)
					}
				}
				slices.Sort(names)
				if !slices.Equal(names, test.wantNames) {
					t.Errorf("%s: actions=%v, want %v", message.Type, names, test.wantNames)
				}
			}
		})
	}
}

func testGeneratedIdentity(t *testing.T, binary string) {
	// Arrange.
	seen := make(map[string]bool)
	for _, ignoredID := range []string{"web-quality-agent-0123abcd", "invalid-identity"} {
		root := t.TempDir()
		env := configuredEnvironment(root)
		env["AGENT_ID"] = ignoredID
		env["CONTROLLER_TYPE"] = "ignored-controller"
		host := startAgent(t, binary, root, env, []string{"--actions=", "--heartbeat-interval=0"})

		// Act.
		started := host.receive(t, messages.AgentStarted, "")
		stop := host.send(t, messages.StopAgent, nil)
		host.receive(t, messages.AgentStopped, stop.ID)
		host.wait(t)

		// Assert.
		if !regexp.MustCompile(`^web-quality-agent-[0-9a-f]{8}$`).MatchString(started.AgentID) || seen[started.AgentID] || started.AgentID == ignoredID {
			t.Fatalf("expected a fresh typed agent ID, got %q", started.AgentID)
		}
		seen[started.AgentID] = true
	}
}

func testHTTPWorkflow(t *testing.T, binary string, flagsOverride bool) {
	// Arrange.
	const body = "offline evidence from the local HTTP fixture"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, "<title>%s</title><a href=\"/missing\">Missing page</a>", body)
	}))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "configured state with spaces")
	env := configuredEnvironment(root)
	args := []string{"--actions=check-links", "--inline-limit=0", "--heartbeat-interval=50ms"}
	var ignoredRoot string
	if flagsOverride {
		ignoredRoot = filepath.Join(t.TempDir(), "unused environment")
		env = configuredEnvironment(ignoredRoot)
		env["LOG_LEVEL"] = "error"
		env["AGENT_ID"] = "invalid-identity"
		env["CONTROLLER_TYPE"] = "invalid type"
		args = append(args, "--bus-url="+fileURL(filepath.Join(root, "bus")), "--storage-url="+fileURL(filepath.Join(root, "storage")), "--log-level=info", "--log-format=text")
	}
	host := startAgent(t, binary, root, env, args)

	// Act.
	started := host.receive(t, messages.AgentStarted, "")
	heartbeat := host.receive(t, messages.AgentHeartbeat, "")
	invalid := host.send(t, messages.RunAction, map[string]any{"action_name": "check-links", "input": map[string]any{"url": "invalid-url"}})
	failure := host.receive(t, messages.CommandFailed, invalid.ID)
	command := host.send(t, messages.RunAction, map[string]any{"action_name": "check-links", "input": map[string]any{"url": server.URL}})
	reply := host.receive(t, messages.ActionSucceeded, command.ID)
	stop := host.send(t, messages.StopAgent, nil)
	stopped := host.receive(t, messages.AgentStopped, stop.ID)
	logs := host.wait(t)

	// Assert.
	type announcement struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Status string `json:"status"`
		Server struct {
			IPAddress string `json:"ip_address"`
			Hostname  string `json:"hostname"`
		} `json:"server"`
		Actions []struct {
			Name string `json:"name"`
		} `json:"actions"`
	}
	var identity, live, terminal announcement
	decode(t, started.Payload, &identity)
	decode(t, heartbeat.Payload, &live)
	decode(t, stopped.Payload, &terminal)
	if !regexp.MustCompile(`^web-quality-agent-[0-9a-f]{8}$`).MatchString(identity.ID) || identity.Type != "web-quality" || identity.ID != started.AgentID {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	var fields map[string]json.RawMessage
	decode(t, started.Payload, &fields)
	if _, present := fields["name"]; present {
		t.Fatal("agent announcement still has a name field")
	}
	if identity.Server.IPAddress != "192.0.2.10" || identity.Server.Hostname != "test-pod" {
		t.Fatalf("server environment missing from announcement: %+v", identity.Server)
	}
	if identity.Status != "ready" || live.Status != "ready" || terminal.Status != "stopped" {
		t.Fatalf("unexpected lifecycle states: %s, %s, %s", identity.Status, live.Status, terminal.Status)
	}
	if len(identity.Actions) != 1 || identity.Actions[0].Name != "check-links" {
		t.Fatalf("explicit action selection was lost: %+v", identity.Actions)
	}
	if heartbeat.AgentID != started.AgentID || stopped.AgentID != started.AgentID || live.ID != identity.ID || live.Type != identity.Type || live.Server != identity.Server {
		t.Fatal("heartbeat or stop changed the runtime identity")
	}
	var errorReply struct {
		Message string `json:"message"`
	}
	decode(t, failure.Payload, &errorReply)
	if !strings.Contains(strings.ToLower(errorReply.Message), "url") || requests.Load() != 2 {
		t.Fatalf("invalid command did not recover for a subsequent request: %+v, requests=%d", errorReply, requests.Load())
	}
	var result struct {
		Output         json.RawMessage `json:"output"`
		OutputArtifact *artifact.Ref   `json:"output_artifact"`
	}
	decode(t, reply.Payload, &result)
	if result.OutputArtifact == nil || len(result.Output) != 0 {
		t.Fatalf("zero inline limit did not persist output: %s", reply.Payload)
	}
	ref := *result.OutputArtifact
	if ref.AgentID != started.AgentID || ref.CommandID != command.ID || ref.ActionName != "check-links" {
		t.Fatalf("artifact reference lost command provenance: %+v", ref)
	}
	store, err := fsartifact.NewArtifactFsStore(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.Get(t.Context(), ref)
	if err != nil {
		t.Fatalf("read persisted output from configured storage: %v", err)
	}
	var response struct {
		URL          string `json:"url"`
		Title        string `json:"title"`
		StatusCode   int    `json:"status_code"`
		CheckedLinks int    `json:"checked_links"`
		BrokenLinks  int    `json:"broken_links"`
		Links        []struct {
			URL        string `json:"url"`
			StatusCode int    `json:"status_code"`
		} `json:"links"`
	}
	decode(t, data, &response)
	if response.URL != server.URL+"/" || response.StatusCode != http.StatusOK || response.Title != body || response.CheckedLinks != 1 || response.BrokenLinks != 1 || len(response.Links) != 1 || response.Links[0].URL != server.URL+"/missing" || response.Links[0].StatusCode != http.StatusNotFound {
		t.Fatalf("stored link check result: %+v", response)
	}
	if flagsOverride {
		if _, err := os.Stat(ignoredRoot); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("flag override wrote into environment root: %v", err)
		}
		if !strings.Contains(logs, "level=INFO") || !strings.Contains(logs, `service=commander-web-quality-agent`) {
			t.Fatalf("logging flags did not override environment: %s", logs)
		}
	} else {
		for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
			var record map[string]any
			decode(t, []byte(line), &record)
			level, validLevel := record["level"].(string)
			if !validLevel || level == "" || record["service"] != "commander-web-quality-agent" || record["agent_type"] != "web-quality" || record["agent_id"] != identity.ID {
				t.Fatalf("invalid structured agent identity log: %s", line)
			}
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if flagsOverride && (!strings.Contains(line, "service=commander-web-quality-agent") || !strings.Contains(line, "agent_type=web-quality") || !strings.Contains(line, "agent_id="+identity.ID)) {
			t.Fatalf("text log is missing agent identity: %s", line)
		}
		for _, private := range []string{"component", "bus_url", "storage_url", "database_url", "adapter_type", "controller_type", fileURL(filepath.Join(root, "bus")), fileURL(filepath.Join(root, "storage"))} {
			if strings.Contains(line, private) {
				t.Fatalf("log contains unexpected field or connection value %q: %s", private, line)
			}
		}
	}
	if !strings.Contains(logs, "agent starting") || !strings.Contains(logs, "agent stopped") || strings.Contains(logs, body) {
		t.Fatalf("missing lifecycle logs or leaked action output: %s", logs)
	}
}

func testPermissions(t *testing.T, binary string) {
	// Arrange.
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1) }))
	defer server.Close()
	root := t.TempDir()
	env := configuredEnvironment(root)
	env["LOG_LEVEL"] = "error"
	host := startAgent(t, binary, root, env, []string{"--actions=check-links", "--permissions=", "--heartbeat-interval=0"})
	host.receive(t, messages.AgentStarted, "")

	// Act.
	command := host.send(t, messages.RunAction, map[string]any{"action_name": "check-links", "input": map[string]any{"url": server.URL}})
	failure := host.receive(t, messages.CommandFailed, command.ID)
	// A retry can already be in flight when the controller sends its receipt.
	// Queue that exact result again so discovery must tolerate the redelivery.
	if err := host.bus.Publish(host.ctx, messages.ControllerEvents("default"), failure); err != nil {
		t.Fatal(err)
	}
	discover := host.send(t, messages.DiscoverActions, nil)
	updated := host.receive(t, messages.AgentUpdated, discover.ID)
	stop := host.send(t, messages.StopAgent, nil)
	host.receive(t, messages.AgentStopped, stop.ID)
	logs := host.wait(t)

	// Assert.
	var errorReply struct {
		Message string `json:"message"`
	}
	decode(t, failure.Payload, &errorReply)
	if !strings.Contains(errorReply.Message, "missing permission") || requests.Load() != 0 {
		t.Fatalf("denied command reached the HTTP service: %+v, requests=%d", errorReply, requests.Load())
	}
	if updated.AgentID != host.agentID {
		t.Fatal("agent did not recover to accept discovery")
	}
	if strings.Contains(logs, "agent starting") || strings.Contains(logs, "agent stopped") {
		t.Fatalf("LOG_LEVEL=error did not suppress informational logs: %s", logs)
	}
}

func testSignal(t *testing.T, binary string, signal os.Signal) {
	// Arrange.
	root := t.TempDir()
	host := startAgent(t, binary, root, configuredEnvironment(root), []string{"--actions=", "--heartbeat-interval=0"})
	host.receive(t, messages.AgentStarted, "")

	// Act.
	if err := host.command.Process.Signal(signal); err != nil {
		t.Fatal(err)
	}
	stopped := host.receive(t, messages.AgentStopped, "")
	logs := host.wait(t)

	// Assert.
	if stopped.AgentID != host.agentID || !strings.Contains(logs, "agent stopped") {
		t.Fatalf("signal did not announce clean shutdown: %+v\n%s", stopped, logs)
	}
}

type agentProcess struct {
	ctx            context.Context
	bus            comms.Bus
	events         comms.Subscription
	command        *exec.Cmd
	done           chan struct{}
	err            error
	stderr         bytes.Buffer // Read only after done closes.
	agentID        string
	manualReceipts bool
	seenResults    map[string]messages.Message
}

func startAgent(t *testing.T, binary, root string, env map[string]string, args []string) *agentProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	events, err := bus.Subscribe(ctx, messages.ControllerEvents("default"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	process := &agentProcess{ctx: ctx, bus: bus, events: events, done: make(chan struct{})}
	process.command = exec.CommandContext(ctx, binary, args...)
	process.command.Env = childEnvironment(env)
	process.command.Dir = t.TempDir()
	process.command.Stdout, process.command.Stderr = &process.stderr, &process.stderr
	process.command.WaitDelay = 2 * time.Second
	if err := process.command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { process.err = process.command.Wait(); close(process.done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-process.done:
		case <-time.After(5 * time.Second):
			t.Error("agent child did not exit after cleanup cancellation")
		}
	})
	return process
}

func (p *agentProcess) receive(t *testing.T, kind messages.MessageType, correlation string) messages.Message {
	t.Helper()
	for {
		message, err := p.events.Next(p.ctx)
		if err != nil {
			select {
			case <-p.done:
				t.Fatalf("waiting for %s: %v; process exit: %v\n%s", kind, err, p.err, p.stderr.String())
			default:
				t.Fatalf("waiting for %s: %v", kind, err)
			}
		}
		if err := p.events.Ack(p.ctx, message.ID); err != nil {
			t.Fatal(err)
		}
		if message.Type == messages.AgentStarted {
			p.agentID = message.AgentID
		}
		if !p.manualReceipts && (message.Type == messages.ActionSucceeded || message.Type == messages.CommandFailed) {
			p.accept(t, message)
		}
		if message.Type == kind && (correlation == "" || message.CorrelationID == correlation) {
			if message.Type == messages.ActionSucceeded || message.Type == messages.CommandFailed {
				if p.seenResults == nil {
					p.seenResults = make(map[string]messages.Message)
				}
				p.seenResults[message.ID] = message
			}
			return message
		}
		// A receipt cannot retract a result already queued by the broker. Ignore
		// only an exact result previously returned to this test, after ACKing
		// this delivery and resending its receipt above. Explicit replay tests
		// still receive duplicates when they request that result.
		if previous, seen := p.seenResults[message.ID]; seen && reflect.DeepEqual(previous, message) {
			continue
		}
		if message.Type != messages.AgentHeartbeat {
			t.Fatalf("expected %s (%s), received %+v", kind, correlation, message)
		}
	}
}

func (p *agentProcess) send(t *testing.T, kind messages.MessageType, payload any) messages.Message {
	t.Helper()
	message, err := messages.NewMessage(kind, p.agentID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.bus.Publish(p.ctx, messages.AgentCommands(p.agentID), message); err != nil {
		t.Fatal(err)
	}
	return message
}

func (p *agentProcess) wait(t *testing.T) string {
	t.Helper()
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatalf("agent exited unsuccessfully: %v\n%s", p.err, p.stderr.String())
		}
		return p.stderr.String()
	case <-p.ctx.Done():
		t.Fatalf("agent did not exit: %v", p.ctx.Err())
		return ""
	}
}

func configuredEnvironment(root string) map[string]string {
	return map[string]string{
		"BUS_URL": fileURL(filepath.Join(root, "bus")), "STORAGE_URL": fileURL(filepath.Join(root, "storage")),
		"DATABASE_URL": "postgres://unused", "LOG_LEVEL": "info", "LOG_FORMAT": "json",
		"SERVER_IP_ADDRESS": " 192.0.2.10 ", "SERVER_HOSTNAME": " test-pod ",
		"CHROMIUM_PATH": "", "CHROMIUM_NO_SANDBOX": "",
	}
}

func childEnvironment(values map[string]string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := values[key]; replace {
			continue
		}
		env = append(env, entry)
	}
	for key, value := range values {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
}

func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func decode(t *testing.T, data []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(data, destination); err != nil {
		t.Fatalf("decode public response: %v\n%s", err, data)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("cannot locate Go module root")
		}
		directory = parent
	}
}

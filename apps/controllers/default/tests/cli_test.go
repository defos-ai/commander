package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"commander/framework/agent"
	"commander/framework/comms"
	adapterevents "commander/framework/comms/clients/adapter/events"
	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/comms/messages"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	commsprovider "commander/providers/comms"
	controllerprovider "commander/providers/controller"
)

var binary string

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	directory, err := os.MkdirTemp("", "commander-controller-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := func() int {
		defer os.RemoveAll(directory)
		binary = filepath.Join(directory, "commander-controller")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-race", "-buildvcs=false", "-o", binary, "./apps/controllers/default/cmd")
		command.Dir = "../../../.."
		if output, err := command.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build: %v\n%s", err, output)
			return 1
		}
		return m.Run()
	}()
	os.Exit(code)
}

type deployment struct {
	ctx      context.Context
	root     string
	env      map[string]string
	bus      comms.Bus
	database *controllerprovider.Database
	reports  *controllerprovider.ReportStorage
}

func arrange(t *testing.T) *deployment {
	t.Helper()
	if testing.Short() {
		t.Skip("process end-to-end test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	env := map[string]string{
		"BUS_URL": filepath.Join(root, "bus"), "STORAGE_URL": filepath.Join(root, "storage"), "DATABASE_URL": filepath.Join(root, "database"),
		"LOG_LEVEL": "info", "LOG_FORMAT": "json", "HEARTBEAT_TIMEOUT": "45s",
		// The concrete default host owns its type and generates a fresh process ID.
		"CONTROLLER_TYPE": "ignored-controller-type", "CONTROLLER_ID": "ignored-controller-id",
		// A hosting environment may define this for other services; workers ignore it.
		"LISTEN_ADDRESS": "invalid-address",
	}
	database, err := controllerprovider.OpenDatabase(ctx, env["DATABASE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	mission := missions.Mission{Name: "review", Objective: "Review the supplied subject", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	if err := database.MissionStore.Upsert(ctx, mission); err != nil {
		t.Fatal(err)
	}
	bus, err := commsprovider.OpenBus(ctx, env["BUS_URL"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	reports, err := controllerprovider.OpenReportStorage(ctx, env["STORAGE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reports.Close() })
	return &deployment{ctx: ctx, root: root, env: env, bus: bus, database: database, reports: reports}
}

type process struct {
	command  *exec.Cmd
	done     chan struct{}
	err      error
	logPath  string
	id       string
	typeName string
}

func (d *deployment) start(t *testing.T, override bool, args ...string) *process {
	t.Helper()
	env := make(map[string]string)
	for key, value := range d.env {
		env[key] = value
	}
	if override {
		for _, field := range []struct{ env, flag string }{
			{"BUS_URL", "bus-url"}, {"STORAGE_URL", "storage-url"}, {"DATABASE_URL", "database-url"},
			{"LOG_LEVEL", "log-level"}, {"LOG_FORMAT", "log-format"},
			{"HEARTBEAT_TIMEOUT", "heartbeat-timeout"},
		} {
			args = append(args, "--"+field.flag+"="+env[field.env])
			env[field.env] = "invalid://environment"
		}
	}
	logPath := filepath.Join(t.TempDir(), "controller.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	command := exec.CommandContext(d.ctx, binary, args...)
	command.Dir = d.root
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, exists := env[key]; !exists {
			command.Env = append(command.Env, entry)
		}
	}
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	command.Stdout, command.Stderr, command.WaitDelay = log, log, 2*time.Second
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	p := &process{command: command, done: make(chan struct{}), logPath: logPath}
	go func() { p.err = command.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = command.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
				t.Error("controller process cleanup timed out")
			}
		}
		if t.Failed() {
			data, _ := os.ReadFile(logPath)
			t.Logf("controller log:\n%s", data)
		}
	})
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, _ := os.ReadFile(logPath)
		for _, line := range bytes.Split(data, []byte("\n")) {
			var entry struct {
				Message string `json:"msg"`
				ID      string `json:"controller_id"`
				Type    string `json:"controller_type"`
			}
			if json.Unmarshal(line, &entry) == nil && entry.Message == "Controller ready" {
				p.id, p.typeName = entry.ID, entry.Type
				var record map[string]any
				if err := json.Unmarshal(line, &record); err != nil {
					t.Fatal(err)
				}
				if record["service"] != "commander-controller" {
					t.Fatalf("unexpected controller service log: %s", line)
				}
				for _, key := range []string{"component", "bus_url", "storage_url", "database_url", "agent_type", "adapter_type"} {
					if _, present := record[key]; present {
						t.Errorf("controller log contains unexpected field %q: %s", key, line)
					}
				}
				for _, key := range []string{"BUS_URL", "STORAGE_URL", "DATABASE_URL"} {
					if bytes.Contains(line, []byte(d.env[key])) {
						t.Errorf("controller log exposes %s: %s", key, line)
					}
				}
				if runtimeType, valid := id.RuntimeType(p.id, id.Controller); !valid || runtimeType != "default" || p.typeName != runtimeType {
					t.Fatalf("unexpected controller identity: id=%q type=%q", p.id, p.typeName)
				}
				return p
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("controller did not become ready")
		case <-p.done:
			t.Fatalf("controller startup failed: %v\n%s", p.err, data)
		case <-ticker.C:
		}
	}
}

func (p *process) stop(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("graceful signal requires Unix")
	}
	if err := p.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatalf("controller shutdown: %v", p.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("controller shutdown timed out")
	}
}

func (d *deployment) client(t *testing.T) *controllercommands.Client {
	t.Helper()
	adapterID, err := id.NewRuntime("test", id.Adapter)
	if err != nil {
		t.Fatal(err)
	}
	client, err := controllercommands.NewClient(d.ctx, d.bus, adapterID, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestControllerCLI(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("flags_override_environment=%t", override), func(t *testing.T) {
			// Arrange: real controller binary with independent broker clients.
			d := arrange(t)
			controller := d.start(t, override)
			client := d.client(t)
			// Act: start an operation and disconnect the submitting adapter.
			catalog, err := d.database.MissionStore.List(d.ctx)
			if err != nil {
				t.Fatal(err)
			}
			start := controllercommands.StartOperationCommand{OperationID: id.OperationID(), MissionName: "review", Input: json.RawMessage(`{"subject":"fixture"}`)}
			operation, err := client.Start(d.ctx, start)
			if err != nil {
				t.Fatal(err)
			}
			if operation.ID != start.OperationID {
				t.Fatalf("operation ID changed in transit: got %s, want %s", operation.ID, start.OperationID)
			}
			if _, err := client.Start(d.ctx, start); !errors.Is(err, operations.ErrOperationConflict) {
				t.Fatalf("expected operation ID conflict across the bus: %v", err)
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			replacement := d.client(t)
			question, err := replacement.Ask(d.ctx, controllercommands.AskQuestionCommand{OperationID: operation.ID, Text: "Proceed?"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := replacement.Answer(d.ctx, controllercommands.AnswerQuestionCommand{OperationID: operation.ID, QuestionID: question.ID, Answer: "yes"}); err != nil {
				t.Fatal(err)
			}
			finished, err := replacement.Complete(d.ctx, controllercommands.CompleteOperationCommand{OperationID: operation.ID, Output: json.RawMessage(`{"summary":"done"}`)})
			if err != nil {
				t.Fatal(err)
			}
			previousID := controller.id
			controller.stop(t)
			controller = d.start(t, false)
			stored, err := d.database.OperationStore.Get(d.ctx, operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			report, err := d.reports.ReportStore.Get(d.ctx, stored.ReportID)
			if err != nil {
				t.Fatal(err)
			}
			// Assert: adapter disconnect and controller restart preserve progress.
			if controller.id == previousID {
				t.Fatalf("replacement controller reused process identity %q", previousID)
			}
			if len(catalog) == 0 || finished.Status != operations.Completed || stored.ReportID != report.ID || stored.ID != operation.ID {
				t.Fatalf("unexpected recovered operation: %+v, report %+v", stored, report)
			}
			if !bytes.Contains(report.Output, []byte("done")) {
				t.Fatalf("missing report output: %+v", report)
			}
			controller.stop(t)
		})
	}
}

func TestControllerIdentityIsNotConfigurable(t *testing.T) {
	for _, argument := range []string{
		"--controller-id=default-controller-0123abcd", "--controller-type=default",
	} {
		t.Run(argument, func(t *testing.T) {
			// Arrange.
			d := arrange(t)
			command := exec.CommandContext(d.ctx, binary, argument)
			command.Dir = t.TempDir()
			for key, value := range d.env {
				command.Env = append(command.Env, key+"="+value)
			}

			// Act.
			output, err := command.CombinedOutput()

			// Assert.
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || len(output) == 0 {
				t.Fatalf("removed identity flag did not fail with an error: %v\n%s", err, output)
			}
		})
	}
}

func TestControllerConnectionErrorsOmitCredentials(t *testing.T) {
	for _, scenario := range []struct{ name, argument string }{
		{"broker", "--bus-url=amqp://user:secret-credential@localhost"},
		{"storage", "--storage-url=s3://bucket?token=secret-token"},
		{"database", "--database-url=mongodb://user:secret-credential@localhost/defos"},
		{"malformed URL", "--bus-url=amqp://user:secret-credential@localhost/%zz?token=secret-token"},
		{"filesystem credentials", "--storage-url=file://user:secret-credential@localhost/state"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange.
			d := arrange(t)
			command := exec.CommandContext(d.ctx, binary, scenario.argument)
			command.Dir = t.TempDir()
			for key, value := range d.env {
				command.Env = append(command.Env, key+"="+value)
			}

			// Act.
			output, err := command.CombinedOutput()

			// Assert.
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || len(output) == 0 {
				t.Fatalf("invalid connection did not produce a failure diagnostic: %v\n%s", err, output)
			}
			if bytes.Contains(output, []byte("secret-")) {
				t.Fatalf("connection diagnostic exposed credentials: %s", output)
			}
		})
	}
}

func TestControllerRequestsRemainResponsive(t *testing.T) {
	// Arrange: discovery waits for an independently hosted agent's response.
	d := arrange(t)
	p := d.start(t, false)
	client := d.client(t)
	agentID := "fixture-agent-1234abcd"
	err := d.database.AgentStore.Upsert(d.ctx, agentmodel.Agent{
		AgentSnapshot: agent.AgentSnapshot{ID: agentID, Type: "fixture", Status: agent.AgentReady},
		LastSeen:      time.Now().UTC(), LastHeartbeat: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	commands, err := d.bus.Subscribe(d.ctx, messages.AgentCommands(agentID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = commands.Close() })
	requests, err := d.bus.Subscribe(d.ctx, messages.ControllerRequests("default"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = requests.Close() })
	waitCtx, cancel := context.WithCancel(d.ctx)
	defer cancel()
	waiting := make(chan error, 1)
	go func() { _, err := client.DiscoverActions(waitCtx, agentID, nil); waiting <- err }()
	request, err := requests.Next(d.ctx)
	if err != nil {
		t.Fatal(err)
	}
	replies, err := d.bus.Subscribe(d.ctx, messages.AdapterReplies(request.AdapterID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replies.Close() })
	if _, err := commands.Next(d.ctx); err != nil {
		t.Fatal(err)
	}
	// Act: an independent command completes while discovery waits, then cancels.
	commandCtx, commandCancel := context.WithTimeout(d.ctx, 3*time.Second)
	defer commandCancel()
	operation, commandErr := client.Start(commandCtx, controllercommands.StartOperationCommand{MissionName: "review", Input: json.RawMessage(`{}`)})
	cancel()
	waitErr := <-waiting
	var cancellation adapterevents.ControllerReply
	for {
		reply, err := replies.Next(d.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if reply.CorrelationID == request.ID {
			if err := json.Unmarshal(reply.Payload, &cancellation); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	_, missingErr := client.Cancel(d.ctx, controllercommands.CancelOperationCommand{OperationID: id.New()})
	// Assert.
	if commandErr != nil || operation.ID == "" {
		t.Fatalf("command blocked behind discovery: %v", commandErr)
	}
	if !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("cancellation identity lost: %v", waitErr)
	}
	if cancellation.Error == nil || cancellation.Error.Code != "canceled" {
		t.Fatalf("controller did not cancel the pending command: %+v", cancellation)
	}
	if !errors.Is(missingErr, operations.ErrUnknownOperation) {
		t.Fatalf("remote domain error identity lost: %v", missingErr)
	}
	p.stop(t)
}

func TestDuplicateAdapterRequestCreatesOneOperation(t *testing.T) {
	// Arrange: deliver the same action twice while the first may still run.
	d := arrange(t)
	p := d.start(t, false)
	adapterID := "mcp-adapter-1234abcd"
	subscription, err := d.bus.Subscribe(d.ctx, messages.AdapterReplies(adapterID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Close() })
	params, _ := json.Marshal(controllercommands.StartOperationCommand{MissionName: "review", Input: json.RawMessage(`{}`)})
	request, err := messages.NewAdapterMessage(messages.AdapterRequest, adapterID, controllercommands.CommandEnvelope{Method: controllercommands.MethodStartOperation, Parameters: params, Deadline: time.Now().Add(10 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	// Act.
	for range 2 {
		if err := d.bus.Publish(d.ctx, messages.ControllerRequests("default"), request); err != nil {
			t.Fatal(err)
		}
	}
	var operationIDs []string
	for range 2 {
		reply, err := subscription.Next(d.ctx)
		if err != nil {
			t.Fatal(err)
		}
		var outcome adapterevents.ControllerReply
		if err := json.Unmarshal(reply.Payload, &outcome); err != nil {
			t.Fatal(err)
		}
		if outcome.Error != nil {
			t.Fatal(outcome.Error.Message)
		}
		var operation operations.Operation
		if err := json.Unmarshal(outcome.Result, &operation); err != nil {
			t.Fatal(err)
		}
		operationIDs = append(operationIDs, operation.ID)
		if err := subscription.Ack(d.ctx, reply.ID); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := d.database.OperationStore.List(d.ctx)
	// Assert.
	if err != nil || len(stored) != 1 || operationIDs[0] != operationIDs[1] {
		t.Fatalf("duplicate action created different operations: %v, %+v, %v", operationIDs, stored, err)
	}
	p.stop(t)
}

func TestInvalidAdapterMessageDoesNotStopController(t *testing.T) {
	// Arrange: malformed request and cancellation payloads on their real topics.
	d := arrange(t)
	p := d.start(t, false)
	adapterID := "mcp-adapter-1234abcd"
	request, err := messages.NewAdapterMessage(messages.AdapterRequest, adapterID, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	cancellation, err := messages.NewAdapterMessage(messages.AdapterRequestCanceled, adapterID, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Act: a valid command follows invalid input on both subscriptions.
	if err := d.bus.Publish(d.ctx, messages.ControllerRequests("default"), request); err != nil {
		t.Fatal(err)
	}
	if err := d.bus.Publish(d.ctx, messages.ControllerCancellations("default"), cancellation); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(d.ctx, 3*time.Second)
	defer cancel()
	_, commandErr := d.client(t).Start(ctx, controllercommands.StartOperationCommand{MissionName: "review", Input: json.RawMessage(`{}`)})
	// Assert.
	if commandErr != nil {
		t.Fatalf("invalid input stopped controller: %v", commandErr)
	}
	p.stop(t)
}

func TestInvalidReplyDoesNotStopAdapterClient(t *testing.T) {
	// Arrange: observe a public request and reply through its advertised route.
	d := arrange(t)
	requests, err := d.bus.Subscribe(d.ctx, messages.ControllerRequests("default"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = requests.Close() })
	client := d.client(t)
	ctx, cancel := context.WithTimeout(d.ctx, 3*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := client.Start(ctx, controllercommands.StartOperationCommand{MissionName: "review", Input: json.RawMessage(`{}`)})
		finished <- err
	}()
	request, err := requests.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := messages.NewAdapterMessage(messages.ControllerResponse, request.AdapterID, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	invalid.CorrelationID = request.ID
	valid, err := messages.NewAdapterMessage(messages.ControllerResponse, request.AdapterID, adapterevents.ControllerReply{Result: json.RawMessage(`{"id":"fixture-operation"}`)})
	if err != nil {
		t.Fatal(err)
	}
	valid.CorrelationID = request.ID
	// Act: a malformed outcome precedes the successful response.
	for _, reply := range []messages.Message{invalid, valid} {
		if err := d.bus.Publish(ctx, messages.AdapterReplies(request.AdapterID), reply); err != nil {
			t.Fatal(err)
		}
	}
	callErr := <-finished
	// Assert: malformed input is retained without terminating the reply consumer.
	if callErr != nil {
		t.Fatalf("invalid reply stopped adapter client: %v", callErr)
	}
}

func TestControllerStopsWhenCoordinationFails(t *testing.T) {
	for _, source := range []string{"agent event", "lease sweep"} {
		t.Run(source, func(t *testing.T) {
			// Arrange: a live worker with real filesystem persistence.
			d := arrange(t)
			if source == "lease sweep" {
				d.env["HEARTBEAT_TIMEOUT"] = "300ms"
			}
			p := d.start(t, false)
			// Act: the inventory directory becomes unreadable by its provider.
			namespace := filepath.Join(d.env["DATABASE_URL"], "agents")
			if err := os.Rename(namespace, namespace+".backup"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(namespace, []byte("unavailable inventory"), 0600); err != nil {
				t.Fatal(err)
			}
			if source == "agent event" {
				agentID := "fixture-agent-1234abcd"
				message, err := messages.NewMessage(messages.AgentHeartbeat, agentID, agent.AgentSnapshot{
					ID: agentID, Type: "fixture", Status: agent.AgentReady,
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := d.bus.Publish(d.ctx, messages.ControllerEvents("default"), message); err != nil {
					t.Fatal(err)
				}
			}
			// Assert: the host reports the failure and exits instead of staying alive.
			select {
			case <-p.done:
				if p.err == nil {
					t.Fatal("controller exited successfully after coordination failed")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("controller remained alive after coordination failed")
			}
			logs, err := os.ReadFile(p.logPath)
			if err != nil || !bytes.Contains(logs, []byte("controller coordination stopped")) {
				t.Fatalf("missing coordination failure in logs: %s, %v", logs, err)
			}
		})
	}
}

package cli_test

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentevents "commander/framework/comms/clients/agent/events"
	"commander/framework/comms/messages"
)

func testRepeatedActionCommand(t *testing.T, binary string) {
	// Arrange.
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("one execution"))
	}))
	defer server.Close()
	root := t.TempDir()
	host := startAgent(t, binary, root, configuredEnvironment(root), []string{"--actions=check-links", "--heartbeat-interval=0"})
	host.receive(t, messages.AgentStarted, "")
	request := host.send(t, messages.RunAction, map[string]any{"action_name": "check-links", "input": map[string]any{"url": server.URL}})
	original := host.receive(t, messages.ActionSucceeded, request.ID)

	// Act. A receipt stops retries, but the agent still remembers the outcome.
	if err := host.bus.Publish(host.ctx, messages.AgentCommands(host.agentID), request); err != nil {
		t.Fatal(err)
	}
	replayed := host.receive(t, messages.ActionSucceeded, request.ID)
	stop := host.send(t, messages.StopAgent, nil)
	host.receive(t, messages.AgentStopped, stop.ID)
	host.wait(t)

	// Assert.
	if requests.Load() != 1 || !reflect.DeepEqual(original, replayed) {
		t.Fatalf("duplicate command repeated work or changed outcome: requests=%d, original=%+v replay=%+v", requests.Load(), original, replayed)
	}
	assertNoExecutionStorage(t, root)
}

func testConflictingActionCommand(t *testing.T, binary string) {
	// Arrange.
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("one execution"))
	}))
	defer server.Close()
	root := t.TempDir()
	host := startAgent(t, binary, root, configuredEnvironment(root), []string{"--actions=check-links", "--heartbeat-interval=0"})
	host.receive(t, messages.AgentStarted, "")
	request := host.send(t, messages.RunAction, map[string]any{"action_name": "check-links", "input": map[string]any{"url": server.URL}})
	host.receive(t, messages.ActionSucceeded, request.ID)
	conflicting, err := messages.NewMessage(messages.RunAction, host.agentID, map[string]any{"action_name": "check-links", "input": map[string]any{"url": server.URL + "/changed"}})
	if err != nil {
		t.Fatal(err)
	}
	conflicting.ID, conflicting.CreatedAt = request.ID, request.CreatedAt

	// Act. The following discovery response proves the duplicate was consumed.
	if err := host.bus.Publish(host.ctx, messages.AgentCommands(host.agentID), conflicting); err != nil {
		t.Fatal(err)
	}
	discovery := host.send(t, messages.DiscoverActions, nil)
	host.receive(t, messages.AgentUpdated, discovery.ID)
	stop := host.send(t, messages.StopAgent, nil)
	host.receive(t, messages.AgentStopped, stop.ID)
	host.wait(t)

	// Assert. No action response was fabricated for the conflicting request.
	if requests.Load() != 1 {
		t.Fatalf("conflicting command executed: requests=%d", requests.Load())
	}
}

func testResultReceipts(t *testing.T, binary string) {
	// Arrange.
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("one execution"))
	}))
	defer server.Close()
	root := t.TempDir()
	host := startAgent(t, binary, root, configuredEnvironment(root), []string{"--actions=check-links", "--heartbeat-interval=0"})
	host.manualReceipts = true
	host.receive(t, messages.AgentStarted, "")
	request := host.send(t, messages.RunAction, map[string]any{"action_name": "check-links", "input": map[string]any{"url": server.URL}})
	original := host.receive(t, messages.ActionSucceeded, request.ID)

	// Act and Assert. Transport ACK alone does not confirm database persistence.
	for _, damage := range []func(*messages.Message){
		func(receipt *messages.Message) { receipt.Payload = []byte(`{"result_id":"invalid"}`) },
		func(receipt *messages.Message) { receipt.Payload = []byte(`{"result_id":"` + request.ID + `"}`) },
		func(receipt *messages.Message) { receipt.CorrelationID = original.ID },
		func(receipt *messages.Message) {
			receipt.Work = messages.WorkContext{OperationID: request.ID, TaskID: original.ID}
		},
		func(receipt *messages.Message) { receipt.AgentID = "other-agent-1234abcd" },
	} {
		receipt := resultReceipt(t, original)
		damage(&receipt)
		if err := host.bus.Publish(host.ctx, messages.AgentReceipts(host.agentID), receipt); err != nil {
			t.Fatal(err)
		}
		retry := host.receive(t, messages.ActionSucceeded, request.ID)
		if !reflect.DeepEqual(original, retry) {
			t.Fatalf("retry changed the saved outcome: original=%+v retry=%+v", original, retry)
		}
	}
	host.accept(t, original)
	quiet, cancel := context.WithTimeout(host.ctx, 1300*time.Millisecond)
	defer cancel()
	if unexpected, err := host.events.Next(quiet); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("result retries continued after receipt: message=%+v error=%v", unexpected, err)
	}
	stop := host.send(t, messages.StopAgent, nil)
	host.receive(t, messages.AgentStopped, stop.ID)
	host.wait(t)
	if requests.Load() != 1 {
		t.Fatalf("result retries invoked action again: requests=%d", requests.Load())
	}
	assertNoExecutionStorage(t, root)
}

func testRetryDuringWork(t *testing.T, binary string) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process signals")
	}
	// Arrange.
	working, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/slow" {
			close(working)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		_, _ = w.Write([]byte("completed"))
	}))
	defer server.Close()
	defer close(release)
	root := t.TempDir()
	host := startAgent(t, binary, root, configuredEnvironment(root), []string{"--actions=check-links", "--heartbeat-interval=0"})
	host.manualReceipts = true
	host.receive(t, messages.AgentStarted, "")
	first := host.send(t, messages.RunAction, map[string]any{"action_name": "check-links", "input": map[string]any{"url": server.URL}})
	original := host.receive(t, messages.ActionSucceeded, first.ID)

	// Act. Keep the second action blocked while observing delivery of the first.
	host.send(t, messages.RunAction, map[string]any{"action_name": "check-links", "input": map[string]any{"url": server.URL + "/slow"}})
	select {
	case <-working:
	case <-host.ctx.Done():
		t.Fatal("second action did not start")
	}
	retry := host.receive(t, messages.ActionSucceeded, first.ID)
	host.accept(t, original)
	// Receipt processing must also progress while the second action is blocked.
	quiet, cancel := context.WithTimeout(host.ctx, 1300*time.Millisecond)
	defer cancel()
	if unexpected, err := host.events.Next(quiet); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("result receipt was blocked by active work: message=%+v error=%v", unexpected, err)
	}
	// A signal cancels active work and joins the result publisher on shutdown.
	if err := host.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	host.receive(t, messages.AgentStopped, "")
	host.wait(t)

	// Assert.
	if !reflect.DeepEqual(original, retry) || requests.Load() != 2 {
		t.Fatalf("result retry repeated work or changed outcome: requests=%d retry=%+v", requests.Load(), retry)
	}
}

func resultReceipt(t *testing.T, result messages.Message) messages.Message {
	t.Helper()
	receipt, err := messages.NewMessage(messages.ActionResultAccepted, result.AgentID, agentevents.ResultReceipt{ResultID: result.ID})
	if err != nil {
		t.Fatal(err)
	}
	receipt.CorrelationID, receipt.Work = result.CorrelationID, result.Work
	return receipt
}

func (p *agentProcess) accept(t *testing.T, result messages.Message) {
	t.Helper()
	if err := p.bus.Publish(p.ctx, messages.AgentReceipts(p.agentID), resultReceipt(t, result)); err != nil {
		t.Fatal(err)
	}
}

func assertNoExecutionStorage(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(filepath.Join(root, "storage"), func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == filepath.Join(root, "storage") {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "executions" {
			t.Errorf("agent created execution persistence at %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func testStopPendingResult(t *testing.T, binary string, acceptBeforeDeadline bool) {
	// Arrange. A failed action outcome needs durable receipt just like success.
	root := t.TempDir()
	host := startAgent(t, binary, root, configuredEnvironment(root), []string{"--actions=", "--heartbeat-interval=0"})
	host.manualReceipts = true
	host.receive(t, messages.AgentStarted, "")
	request := host.send(t, messages.RunAction, map[string]any{"action_name": "missing", "input": map[string]any{}})
	original := host.receive(t, messages.CommandFailed, request.ID)

	// Act. Stop arrives while the controller has not saved the action result.
	stop := host.send(t, messages.StopAgent, nil)
	if acceptBeforeDeadline {
		retry := host.receive(t, messages.CommandFailed, request.ID)
		if !reflect.DeepEqual(original, retry) {
			t.Fatalf("graceful stop replaced the pending outcome: %+v", retry)
		}
		host.accept(t, original)
	} else {
		for {
			message, err := host.events.Next(host.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := host.events.Ack(host.ctx, message.ID); err != nil {
				t.Fatal(err)
			}
			if message.Type == messages.CommandFailed && message.CorrelationID == stop.ID {
				var failure struct {
					Message string `json:"message"`
				}
				decode(t, message.Payload, &failure)
				if !strings.Contains(failure.Message, "pending action results") {
					t.Fatalf("stop failed for unexpected reason: %+v", failure)
				}
				break
			}
			if !reflect.DeepEqual(original, message) {
				t.Fatalf("agent stopped or changed pending result before acceptance: %+v", message)
			}
		}
		host.accept(t, original)
		// A later stop can succeed after the pending result reaches storage.
		stop = host.send(t, messages.StopAgent, nil)
	}

	// Assert. A successful stopped response follows acceptance, including retry
	// after a bounded drain timeout; no result is discarded by graceful stop.
	host.receive(t, messages.AgentStopped, stop.ID)
	host.wait(t)
}

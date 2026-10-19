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
	"sync"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

// Exercise automatic proposals through the real API, controller, filesystem
// providers and two worker processes. The peer represents a slow external resource.
func TestE2EAutomaticSessionCreationReservesScope(t *testing.T) {
	root, err := filepath.Abs("../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	binaries := make(map[string]string)
	for name, source := range map[string]string{
		"api":        "./apps/adapters/api/cmd/commander-api",
		"controller": "./apps/controllers/default/cmd",
		"worker":     "./apps/adapters/api/tests/e2e/fixtures/sessionworker",
	} {
		binaries[name] = filepath.Join(state, name)
		command := exec.Command("go", "build", "-o", binaries[name], source)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, output)
		}
	}
	entered := make(chan string, 8)
	release := make(chan struct{})
	publish := make(chan struct{})
	var publishOnce sync.Once
	publishInventory := func() { publishOnce.Do(func() { close(publish) }) }
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/inventory" {
			select {
			case <-publish:
				w.WriteHeader(http.StatusNoContent)
			case <-r.Context().Done():
			}
			return
		}
		entered <- r.URL.Path
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(peer.Close)
	t.Cleanup(unblock)
	t.Cleanup(publishInventory)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	mission := filepath.Join(state, "missions.json")
	if err := os.WriteFile(mission, []byte(`[{"name":"session-e2e","objective":"Connect one owned resource","required_actions":["propose-connections","connect-resource"],"starting_actions":["propose-connections"],"input_schema":{"type":"object"},"output_schema":{"type":"object"}}]`), 0600); err != nil {
		t.Fatal(err)
	}
	password := id.New()
	env := append(os.Environ(), "BUS_URL="+filepath.Join(state, "bus"), "DATABASE_URL="+filepath.Join(state, "database"),
		"STORAGE_URL="+filepath.Join(state, "storage"), "API_USERNAME=e2e", "API_PASSWORD="+password, "LOG_LEVEL=error")
	startE2E(t, binaries["api"], []string{"--listen-address=" + address}, env)
	controllerArgs := []string{"--with-missions=" + mission}
	stopController := startE2E(t, binaries["controller"], controllerArgs, env)
	for i := range 2 {
		workerEnv := append(append([]string{}, env...), fmt.Sprintf("FIXTURE_AGENT_ID=fixture-agent-%08x", i+1), "FIXTURE_PEER="+peer.URL)
		startE2E(t, binaries["worker"], nil, workerEnv)
	}
	client := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil}}
	t.Cleanup(client.CloseIdleConnections)
	url := "http://" + address
	waitUntil(t, 10*time.Second, func() bool {
		response, err := client.Get(url + "/readyz")
		if err != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == 200
	})
	request := func(method, path string, body any, want int, result any) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, url+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("e2e", password)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, data)
		}
		if result != nil {
			if err := json.Unmarshal(data, result); err != nil {
				t.Fatal(err)
			}
		}
	}
	waitUntil(t, 10*time.Second, func() bool {
		var catalog struct{ Items []struct{ Status string } }
		request("GET", "/api/v1/agents", nil, 200, &catalog)
		return len(catalog.Items) == 2 && catalog.Items[0].Status == "ready" && catalog.Items[1].Status == "ready"
	})
	input := map[string]any{"session_scope": id.New()}
	start := func() string {
		var operation operations.Operation
		request("POST", "/api/v1/operations", map[string]any{"mission_name": "session-e2e", "input": input}, 201, &operation)
		path := "/api/v1/operations/" + operation.ID
		request("POST", path+"/tasks", map[string]any{"action_name": "propose-connections", "input": input}, 201, nil)
		return path
	}
	first := start()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("automatic proposal never reached a worker")
	}
	assertReserved := func(duration time.Duration) {
		t.Helper()
		select {
		case extra := <-entered:
			t.Fatalf("second connection escaped scope reservation: %s", extra)
		case <-time.After(duration):
		}
	}
	assertReserved(time.Second)
	second := start()
	assertReserved(time.Second)
	request("POST", second+"/tasks", map[string]any{"action_name": "connect-resource", "input": input}, 422, nil)
	// Reservation is derived from saved work, so controller restart cannot release it.
	stopController()
	stopController = startE2E(t, binaries["controller"], controllerArgs, env)
	assertReserved(time.Second)
	unblock()
	waitUntil(t, 10*time.Second, func() bool {
		var operation operations.Operation
		request("GET", first, nil, 200, &operation)
		for _, task := range operation.Tasks {
			if task.ActionName == "connect-resource" && task.Status == operations.TaskSucceeded {
				return true
			}
		}
		return false
	})
	// Accepted results retain ownership before the first inventory snapshot.
	assertReserved(time.Second)
	request("POST", first+"/cancel", map[string]any{}, 200, nil)
	stopController()
	startE2E(t, binaries["controller"], controllerArgs, env)
	assertReserved(time.Second)
	publishInventory()
	waitUntil(t, 5*time.Second, func() bool {
		var catalog struct {
			Items []struct{ Sessions []json.RawMessage }
		}
		request("GET", "/api/v1/agents", nil, 200, &catalog)
		count := 0
		for _, worker := range catalog.Items {
			count += len(worker.Sessions)
		}
		return count == 1
	})
	assertReserved(time.Second)
	for _, path := range []string{first, second} {
		var operation operations.Operation
		request("GET", path, nil, 200, &operation)
		queued, succeeded := 0, 0
		for _, task := range operation.Tasks {
			if task.ActionName != "connect-resource" {
				continue
			}
			if task.Status == operations.TaskQueued {
				queued++
			}
			if task.Status == operations.TaskSucceeded {
				succeeded++
			}
		}
		if path == first && (queued != 0 || succeeded != 1) || path == second && (queued != 2 || succeeded != 0) {
			t.Fatalf("unexpected proposed connection outcomes: %+v", operation.Tasks)
		}
	}
}

func waitUntil(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("public state did not become ready")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func startE2E(t *testing.T, binary string, args, env []string) func() {
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
	return stop
}

package fixtures

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type NativeStatus struct {
	Status     string `json:"status"`
	InstanceID string `json:"instance_id"`
	PID        int    `json:"pid"`
	MCPURL     string `json:"mcp_url"`
	APIURL     string `json:"api_url"`
	AdminURL   string `json:"admin_url"`
}

type NativeCommander struct {
	Executable string
	Profile    string

	directory   string
	environment []string
	client      *http.Client
}

func NewNativeCommander(t *testing.T, executable string) *NativeCommander {
	t.Helper()
	directory := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	f := &NativeCommander{
		Executable: executable, Profile: filepath.Join(directory, "profile"), directory: directory,
		client: &http.Client{Transport: transport, Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "TEMP", "TMP", "TMPDIR":
			f.environment = append(f.environment, entry)
		}
	}
	// Child processes have neither development tools nor service/model credentials.
	f.environment = append(f.environment, "PATH="+directory, "GOMAXPROCS=2", "GOMEMLIMIT=256MiB",
		"API_LISTEN_ADDRESS=127.0.0.1:0", "MCP_LISTEN_ADDRESS="+address, "UI_LISTEN_ADDRESS=127.0.0.1:0")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		if _, err := f.run(ctx, "stop", "--json"); err != nil {
			t.Errorf("stop installed Commander: %v", err)
		}
	})
	return f
}

func (f *NativeCommander) Run(t *testing.T, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()
	output, err := f.run(ctx, args...)
	if err != nil {
		t.Fatalf("installed Commander %s failed: %v", args[0], err)
	}
	return output
}

func (f *NativeCommander) run(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, f.Executable, append(args, "--state-dir", f.Profile)...)
	command.Dir, command.Env, command.WaitDelay = f.directory, f.environment, time.Second
	// Credential lookups intentionally return private stdout; never dump it on failure.
	return command.Output()
}

func (f *NativeCommander) Status(t *testing.T, command string) NativeStatus {
	t.Helper()
	data := f.Run(t, command, "--json")
	var status NativeStatus
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("decode Commander %s status: %v", command, err)
	}
	return status
}

func (f *NativeCommander) Get(t *testing.T, endpoint string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatalf("reach installed Commander: %v", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

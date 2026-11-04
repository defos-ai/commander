package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

func (s *TransientSecretStack) StartReusableSecretAgent(t *testing.T, agentType string, executor *fakes.ReusableSecretAction) (string, *fakes.SecretLogs) {
	t.Helper()
	root := filepath.Dir(s.database)
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := fsartifacts.NewArtifactFileFsStore(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := 500 * time.Millisecond
	logs := &fakes.SecretLogs{}
	runtime, err := agent.NewAgent(agent.AgentConfig{
		ID: agentType + "-agent-00000001", Type: agent.AgentType(agentType), Actions: []action.Executor{executor},
		ArtifactFileStore: files, HeartbeatInterval: &heartbeat, Logger: slog.New(slog.NewTextHandler(logs, nil)), Secrets: &agent.SecretConfig{
			RequestTTL: 15 * time.Second, RefreshInterval: 5 * time.Second, RetryInterval: 100 * time.Millisecond,
		}, Server: agent.ServerInfo{Hostname: "reusable-fixture", IPAddress: "127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(s.Context)
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(ctx, bus, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Error("reusable secret agent did not shut down cleanly")
			}
		case <-time.After(3 * time.Second):
			t.Error("reusable secret agent did not stop")
		}
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	s.Await(t, func() bool {
		response := s.API.Request(t, http.MethodGet, "/api/v1/agents/"+runtime.ID(), Authenticate)
		var observed models.AgentResponse
		return response.Status == http.StatusOK && json.Unmarshal([]byte(response.Body), &observed) == nil && observed.Status == "ready" && !observed.Stale
	})
	return runtime.ID(), logs
}

func (s *TransientSecretStack) AssertNoStoredReusablePlaintext(t *testing.T, values ...string) {
	t.Helper()
	for _, name := range []string{"secret-requests.json", "secrets.json", "operations.json", "executions.json"} {
		data, err := os.ReadFile(filepath.Join(s.database, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if bytes.Contains(data, []byte(value)) || bytes.Contains(data, []byte(base64.StdEncoding.EncodeToString([]byte(value)))) {
				t.Fatal("reusable default entered database records without encryption")
			}
		}
	}
}

package playground_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/host"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSecretFormSavesOutsideChatAndPreservesDraft(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const password = "private-api-login-password"
	values := map[string]string{"GITHUB_TOKEN": " github-private-token ", "OPENAI_API_KEY": "provider-private-key"}
	var writes atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		username, supplied, ok := request.BasicAuth()
		if !ok || username != "commander" || supplied != password {
			t.Error("secret form did not authenticate directly to the API")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/secrets" {
			t.Error("unexpected secret mutation")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var input struct {
			AgentType string `json:"agent_type"`
			Name      string `json:"name"`
			Value     string `json:"value"`
		}
		if json.NewDecoder(request.Body).Decode(&input) != nil || input.AgentType != "builder-agent" || input.Value != values[input.Name] {
			t.Error("secret form changed a scoped value")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writes.Add(1)
		w.WriteHeader(http.StatusCreated)
		writeSecretFormMetadata(w, input.Name)
	}))
	defer api.Close()
	u, publish, _ := secretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: password}}, password, values["GITHUB_TOKEN"], values["OPENAI_API_KEY"])
	defer u.model.Close()
	op := secretFormOperation("GITHUB_TOKEN", "OPENAI_API_KEY")
	publish(op)
	u.text("/watch " + op.ID)
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	const draft = "Keep my unsent chat draft"
	u.text(draft)
	u.wait("automatic multi-secret form", func(view string) bool {
		return strings.Contains(view, "builder-agent · GITHUB_TOKEN") && strings.Contains(view, "builder-agent · OPENAI_API_KEY")
	})
	u.text(values["GITHUB_TOKEN"])
	u.key(tea.KeyMsg{Type: tea.KeyTab})
	u.text(values["OPENAI_API_KEY"])
	for _, value := range values {
		if strings.Contains(u.model.View(), value) {
			t.Fatal("secret input was not masked")
		}
	}
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("confirmed secret saves", func(view string) bool { return strings.Contains(view, "Secrets saved. Waiting for the agent.") })
	if writes.Load() != 2 {
		t.Fatal("multi-secret form did not save each value once")
	}
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(u.model.View(), draft) || !strings.Contains(u.model.View(), "secrets needed") {
		t.Fatal("save lost the chat draft or claimed the agent was ready")
	}
	u.key(tea.KeyMsg{Type: tea.KeyCtrlA})
	u.key(tea.KeyMsg{Type: tea.KeyCtrlK})
	u.text("Normal message after secret entry")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("chat after secret form", func(view string) bool { return strings.Contains(view, "Normal chat remains available") })
	op.Status, op.UpdatedAt, op.Preflight.Ready = "planning", op.UpdatedAt.Add(time.Second), true
	op.Preflight.Version++
	publish(op)
	u.wait("fresh agent readiness", func(view string) bool { return !strings.Contains(view, "secrets needed") })
}

func TestSecretFormDismissalAndManualReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var writes atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer api.Close()
	u, publish, reads := secretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: "fixture-password"}}, "discarded-private-token")
	defer u.model.Close()
	op := secretFormOperation("GITHUB_TOKEN")
	publish(op)
	u.text("/watch " + op.ID)
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("secret field", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	u.text("discarded-private-token")
	u.key(tea.KeyMsg{Type: tea.KeyEsc})
	op.UpdatedAt = op.UpdatedAt.Add(time.Second)
	op.Preflight.Version++
	publish(op)
	target := reads.Load() + 2
	u.wait("repeated requirement snapshots", func(string) bool { return reads.Load() >= target })
	if strings.Contains(u.model.View(), "Agent secrets") || writes.Load() != 0 {
		t.Fatal("dismissed requirement reopened or saved itself")
	}
	u.text("/secrets")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("manually reopened secret form", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	if strings.Contains(u.model.View(), strings.Repeat("•", len("discarded-private-token"))) {
		t.Fatal("dismissed value was retained when the form reopened")
	}
	op.UpdatedAt, op.Preflight.Ready = op.UpdatedAt.Add(time.Second), true
	op.Preflight.Version++
	publish(op)
	u.wait("requirement cleared while editing", func(view string) bool { return strings.Contains(view, "These secrets are no longer required.") })
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	if strings.Contains(u.model.View(), "Agent secrets") || writes.Load() != 0 {
		t.Fatal("cleared requirement stayed open or saved a discarded value")
	}
}

func TestSecretFormDoesNotOverwriteAvailableValues(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var writes atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		writeSecretFormMetadata(w, "GITHUB_TOKEN")
	}))
	defer api.Close()
	u, publish, _ := secretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: "fixture-password"}})
	defer u.model.Close()
	op := secretFormOperation("GITHUB_TOKEN")
	publish(op)
	u.text("/watch " + op.ID)
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("saved secret waiting for delivery", func(view string) bool { return strings.Contains(view, "Secrets are saved. Waiting for the agent.") })
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	if writes.Load() != 0 || !strings.Contains(u.model.View(), "secrets needed") {
		t.Fatal("available metadata caused an overwrite or claimed agent readiness")
	}
}

func TestSecretFormConnectsRemoteAPIWithoutModelCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const password = "remote-api-private-password"
	const token = "remote-saved-private-token"
	var writes atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, supplied, ok := r.BasicAuth()
		if !ok || username != "commander" || supplied != password {
			t.Error("remote form did not reuse its locally entered API login")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var input struct{ Value string }
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&input) != nil || input.Value != token {
			t.Error("remote form did not send the secret directly to the API")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writes.Add(1)
		w.WriteHeader(http.StatusCreated)
		writeSecretFormMetadata(w, "GITHUB_TOKEN")
	}))
	defer api.Close()
	u, publish, _ := secretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{}, password, token, api.URL)
	defer u.model.Close()
	op := secretFormOperation("GITHUB_TOKEN")
	publish(op)
	u.text("/watch " + op.ID)
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("remote API login form", func(view string) bool { return strings.Contains(view, "Connect to API") })
	u.text(api.URL)
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.key(tea.KeyMsg{Type: tea.KeyEnter}) // Default username is commander.
	u.text(password)
	if strings.Contains(u.model.View(), password) {
		t.Fatal("remote API password was not masked")
	}
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("remote secret input", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	u.text(token)
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("remote secret saved", func(view string) bool { return strings.Contains(view, "Secrets saved. Waiting for the agent.") })
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.text("Normal message after remote secret entry")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("chat after remote form", func(view string) bool { return strings.Contains(view, "Normal chat remains available") })
	if writes.Load() != 1 {
		t.Fatal("remote secret was not saved exactly once")
	}
}

func TestSecretFormFailedSaveDoesNotRetryOrRetainValue(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const value = "private-token-from-rejected-save"
	var writes atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writes.Add(1)
		http.Error(w, value, http.StatusServiceUnavailable)
	}))
	defer api.Close()
	u, publish, reads := secretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: "fixture-password"}}, value)
	defer u.model.Close()
	op := secretFormOperation("GITHUB_TOKEN")
	publish(op)
	u.text("/watch " + op.ID)
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("secret field", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	u.text(value)
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("unconfirmed save", func(view string) bool { return strings.Contains(view, "secret save could not be confirmed") })
	if strings.Contains(u.model.View(), value) || writes.Load() != 1 {
		t.Fatal("failed save disclosed the API response or retried automatically")
	}
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	target := reads.Load() + 2
	u.wait("updates after failed save", func(string) bool { return reads.Load() >= target })
	if writes.Load() != 1 || strings.Contains(u.model.View(), "Agent secrets") {
		t.Fatal("returning to chat retried or reopened the failed save")
	}
	u.text("/secrets")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("reloaded form", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	if strings.Contains(u.model.View(), strings.Repeat("•", len(value))) {
		t.Fatal("failed save value was retained in the reloaded form")
	}
	u.key(tea.KeyMsg{Type: tea.KeyEsc})
	u.text("Normal message after failed secret save")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	u.wait("chat after failed save", func(view string) bool { return strings.Contains(view, "Normal chat remains available") })
}

func secretFormOperation(names ...string) bridge.Operation {
	var requirements []actions.Requirement
	for _, name := range names {
		requirements = append(requirements, actions.Requirement{Kind: actions.SecretRequirement, Name: name})
	}
	return bridge.Operation{ID: "secret-form-operation", Status: "waiting", UpdatedAt: time.Now(), Preflight: &bridge.Preflight{Version: 1, Actions: []bridge.ActionReadiness{{UnavailableAgents: []bridge.AgentReadiness{{AgentType: "builder-agent", MissingRequirements: requirements}}}}}}
}

func writeSecretFormMetadata(w http.ResponseWriter, name string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"id": "da378c90-63eb-46da-b186-552e1e144510", "agent_type": "builder-agent", "name": name, "version": 1, "status": "available"})
}

func secretFormUI(t *testing.T, ctx context.Context, api ui.Connection[host.BasicCredentials], private ...string) (*uiDriver, func(bridge.Operation), *atomic.Int32) {
	t.Helper()
	var mu sync.Mutex
	var snapshot string
	publish := func(operation bridge.Operation) {
		t.Helper()
		encoded, err := json.Marshal(operation)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		snapshot = string(encoded)
		mu.Unlock()
	}
	assertPrivate := func(body []byte) {
		for _, value := range private {
			if bytes.Contains(body, []byte(value)) {
				t.Error("local secret form leaked private data outside the API")
			}
		}
	}
	reads := &atomic.Int32{}
	server := mcp.NewServer(&mcp.Implementation{Name: "secret-form-fixture", Version: "1"}, nil)
	for _, name := range []string{"get_operation", "operation_events"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			assertPrivate(request.Params.Arguments)
			payload := `{"events":[],"next_cursor":""}`
			if request.Params.Name == "get_operation" {
				mu.Lock()
				payload = snapshot
				mu.Unlock()
				reads.Add(1)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: payload}}}, nil
		})
	}
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(endpoint.Close)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error("could not inspect model request")
		}
		assertPrivate(body)
		content := "Operation reviewed"
		if bytes.Contains(body, []byte("Normal message after")) {
			content = "Normal chat remains available"
		}
		writeModelDelta(w, map[string]any{"content": content}, "stop")
	}))
	t.Cleanup(provider.Close)
	u := newUIWithConfig(t, ctx, ui.Config{Provider: "other", Model: "fixture", BaseURL: provider.URL, MCP: ui.Connection[host.BearerCredentials]{URL: endpoint.URL}, Keys: map[string]string{"other": "test-only"}, NoColor: true, API: api})
	u.model.Update(tea.WindowSizeMsg{Width: 140, Height: 60})
	return u, publish, reads
}

package playground_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	fakes "github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/host"
)

func TestSecretFormSavesOutsideChatAndPreservesDraft(t *testing.T) {
	for _, source := range []string{"preflight", "running task"} {
		t.Run(source, func(t *testing.T) { testSecretFormSavesOutsideChatAndPreservesDraft(t, source == "running task") })
	}
}

func testSecretFormSavesOutsideChatAndPreservesDraft(t *testing.T, runtime bool) {
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
		if fakes.MissingSecretFormMetadata(w, request, "GITHUB_TOKEN", "OPENAI_API_KEY") {
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
		fakes.WriteSecretFormMetadata(w, input.Name)
	}))
	defer api.Close()
	u, operations := fixtures.SecretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: password}}, password, values["GITHUB_TOKEN"], values["OPENAI_API_KEY"])
	defer u.Close()
	op := fixtures.SecretFormOperation("GITHUB_TOKEN", "OPENAI_API_KEY")
	if runtime {
		op = fixtures.RuntimeSecretFormOperation("GITHUB_TOKEN", "OPENAI_API_KEY")
	}
	operations.Publish(op)
	u.Text("/watch " + op.ID)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	const draft = "Keep my unsent chat draft"
	u.Text(draft)
	u.Wait("automatic multi-secret form", func(view string) bool {
		return strings.Contains(view, "builder-agent · GITHUB_TOKEN") && strings.Contains(view, "builder-agent · OPENAI_API_KEY")
	})
	u.Text(values["GITHUB_TOKEN"])
	u.Key(tea.KeyMsg{Type: tea.KeyTab})
	u.Text(values["OPENAI_API_KEY"])
	for _, value := range values {
		if strings.Contains(u.Model.View(), value) {
			t.Fatal("secret input was not masked")
		}
	}
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("confirmed secret saves", func(view string) bool { return strings.Contains(view, "Secrets saved. Waiting for the agent.") })
	if writes.Load() != 2 {
		t.Fatal("multi-secret form did not save each value once")
	}
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(u.Model.View(), draft) || !strings.Contains(u.Model.View(), "secrets needed") {
		t.Fatal("save lost the chat draft or claimed the agent was ready")
	}
	u.Key(tea.KeyMsg{Type: tea.KeyCtrlA})
	u.Key(tea.KeyMsg{Type: tea.KeyCtrlK})
	u.Text("Normal message after secret entry")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("chat after secret form", func(view string) bool { return strings.Contains(view, "Normal chat remains available") })
	if runtime {
		// Installation updates the agent observation without changing the saved
		// operation timestamp. The watcher must still clear the local prompt.
		op.Tasks[0].MissingRequirements = nil
	} else {
		op.Status, op.UpdatedAt, op.Preflight.Ready = "planning", op.UpdatedAt.Add(time.Second), true
		op.Preflight.Version++
	}
	operations.Publish(op)
	u.Wait("fresh agent readiness", func(view string) bool { return !strings.Contains(view, "secrets needed") })
	if !runtime {
		// A ready preflight revision also schedules model review. Await it so
		// teardown cannot cancel the request while the fixture inspects its body.
		u.Wait("ready preflight review", func(view string) bool { return strings.Contains(view, "Ready preflight reviewed") })
	}
}

func TestSecretFormDismissalAndManualReopen(t *testing.T) {
	for _, source := range []string{"preflight", "running task"} {
		t.Run(source, func(t *testing.T) { testSecretFormDismissalAndManualReopen(t, source == "running task") })
	}
}

func testSecretFormDismissalAndManualReopen(t *testing.T, runtime bool) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var writes atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		if !fakes.MissingSecretFormMetadata(w, r, "GITHUB_TOKEN") {
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	u, operations := fixtures.SecretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: "fixture-password"}}, "discarded-private-token")
	defer u.Close()
	op := fixtures.SecretFormOperation("GITHUB_TOKEN")
	if runtime {
		op = fixtures.RuntimeSecretFormOperation()
	}
	operations.Publish(op)
	u.Text("/watch " + op.ID)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if runtime {
		u.Wait("running task before secret request", func(string) bool { return operations.ReadCount() >= 1 })
		op.Tasks[0].MissingRequirements = []actions.Requirement{{Kind: actions.SecretRequirement, Name: "GITHUB_TOKEN"}}
		operations.Publish(op)
	}
	u.Wait("secret field", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	u.Text("discarded-private-token")
	u.Key(tea.KeyMsg{Type: tea.KeyEsc})
	if !runtime {
		op.UpdatedAt = op.UpdatedAt.Add(time.Second)
		op.Preflight.Version++
	}
	operations.Publish(op)
	target := operations.ReadCount() + 2
	u.Wait("repeated requirement snapshots", func(string) bool { return operations.ReadCount() >= target })
	if strings.Contains(u.Model.View(), "Agent secrets") || writes.Load() != 0 {
		t.Fatal("dismissed requirement reopened or saved itself")
	}
	if runtime && strings.Contains(u.Model.View(), "Operation reviewed") {
		t.Fatal("runtime requirement snapshots triggered model review")
	}
	u.Text("/secrets")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("manually reopened secret form", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	if strings.Contains(u.Model.View(), strings.Repeat("•", len("discarded-private-token"))) {
		t.Fatal("dismissed value was retained when the form reopened")
	}
	if runtime {
		op.Tasks[0].Status = "succeeded"
		op.Status, op.UpdatedAt = "completed", op.UpdatedAt.Add(time.Second)
		op.Progress = bridge.Progress{TaskCounts: map[string]int{"succeeded": 1}, TotalTasks: 1, FinishedTasks: 1, Settled: true}
	} else {
		op.UpdatedAt, op.Preflight.Ready = op.UpdatedAt.Add(time.Second), true
		op.Preflight.Version++
	}
	operations.Publish(op)
	u.Wait("requirement cleared while editing", func(view string) bool { return strings.Contains(view, "These secrets are no longer required.") })
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if strings.Contains(u.Model.View(), "Agent secrets") || writes.Load() != 0 {
		t.Fatal("cleared requirement stayed open or saved a discarded value")
	}
	// Clearing requirements schedules a preflight or completion review. Let it
	// finish before teardown cancels the model request inspected by the fixture.
	review := "Ready preflight reviewed"
	if runtime {
		review = "Operation reviewed"
	}
	u.Wait("review after secret requirements clear", func(view string) bool {
		return strings.Contains(view, review) && !strings.Contains(view, "Thinking")
	})
}

func TestSecretFormDoesNotOverwriteAvailableValues(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var writes atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		fakes.WriteSecretFormMetadata(w, "GITHUB_TOKEN")
	}))
	defer api.Close()
	u, operations := fixtures.SecretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: "fixture-password"}})
	defer u.Close()
	op := fixtures.RuntimeSecretFormOperation("GITHUB_TOKEN")
	operations.Publish(op)
	u.Text("/watch " + op.ID)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("saved secret waiting for delivery", func(view string) bool { return strings.Contains(view, "Secrets are saved. Waiting for the agent.") })
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if writes.Load() != 0 || !strings.Contains(u.Model.View(), "secrets needed") {
		t.Fatal("available metadata caused an overwrite or claimed agent readiness")
	}
}

func TestAdminSecretFormRotatesWithoutWatchedOperation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	const password = "private-admin-api-password"
	const value = "private-replacement-token"
	const secretID = "da378c90-63eb-46da-b186-552e1e144510"
	var writes atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, supplied, ok := r.BasicAuth()
		if !ok || username != "commander" || supplied != password {
			t.Error("Admin secret form did not authenticate with its local login")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/v1/secrets/builder-agent/GITHUB_TOKEN" {
			t.Error("Admin secret form used an unexpected secret scope")
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			fakes.WriteSecretFormMetadata(w, "GITHUB_TOKEN")
			return
		}
		var input struct {
			ID      string `json:"id"`
			Version uint64 `json:"version"`
			Value   string `json:"value"`
		}
		if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&input) != nil || input.ID != secretID || input.Version != 1 || input.Value != value {
			t.Error("Admin secret form did not rotate the metadata shown to the operator")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writes.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": secretID, "agent_type": "builder-agent", "name": "GITHUB_TOKEN", "version": 2, "status": "available"})
	}))
	defer api.Close()
	u, _ := fixtures.SecretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{}, password, value, api.URL)
	defer u.Close()
	u.Text("/admin secrets set builder-agent GITHUB_TOKEN")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("Admin API login", func(view string) bool { return strings.Contains(view, "Connect to API") })
	u.Text(api.URL)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Text(password)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("available secret opened for rotation", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	afterRefresh := time.Now().Add(280 * time.Millisecond)
	u.Wait("manual form retained across requirement refresh", func(view string) bool {
		if !strings.Contains(view, "builder-agent · GITHUB_TOKEN") {
			t.Fatal("an unrelated requirement refresh closed the manual secret form")
		}
		return time.Now().After(afterRefresh)
	})
	u.Text(value)
	if strings.Contains(u.Model.View(), value) {
		t.Fatal("Admin secret value was not masked")
	}
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("Admin secret rotation", func(view string) bool { return strings.Contains(view, "Secret saved.") })
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if writes.Load() != 1 {
		t.Fatal("Admin secret form did not rotate exactly once")
	}
	u.Text("Normal message after Admin secret rotation")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("chat after Admin secret form", func(view string) bool { return strings.Contains(view, "Normal chat remains available") })
}

func TestSecretFormClearsExpiredTaskWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var writes atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		if !fakes.MissingSecretFormMetadata(w, r, "GITHUB_TOKEN") {
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	u, operations := fixtures.SecretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: "fixture-password"}}, "expired-private-token")
	defer u.Close()
	op := fixtures.RuntimeSecretFormOperation("GITHUB_TOKEN")
	deadline := time.Now().Add(time.Second)
	op.Tasks[0].DeadlineAt = &deadline
	operations.Publish(op)
	u.Text("/watch " + op.ID)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("secret field before task timeout", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	u.Text("expired-private-token")
	// The server keeps returning this same snapshot, including its old wait.
	u.Wait("expired task wait cleared", func(view string) bool { return strings.Contains(view, "These secrets are no longer required.") })
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if writes.Load() != 0 || strings.Contains(u.Model.View(), "secrets needed") {
		t.Fatal("expired runtime wait prompted for or saved a value")
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
		if fakes.MissingSecretFormMetadata(w, r, "GITHUB_TOKEN") {
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
		fakes.WriteSecretFormMetadata(w, "GITHUB_TOKEN")
	}))
	defer api.Close()
	u, operations := fixtures.SecretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{}, password, token, api.URL)
	defer u.Close()
	op := fixtures.SecretFormOperation("GITHUB_TOKEN")
	operations.Publish(op)
	u.Text("/watch " + op.ID)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("remote API login form", func(view string) bool { return strings.Contains(view, "Connect to API") })
	u.Text(api.URL)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Key(tea.KeyMsg{Type: tea.KeyEnter}) // Default username is commander.
	u.Text(password)
	if strings.Contains(u.Model.View(), password) {
		t.Fatal("remote API password was not masked")
	}
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("remote secret input", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	u.Text(token)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("remote secret saved", func(view string) bool { return strings.Contains(view, "Secrets saved. Waiting for the agent.") })
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Text("Normal message after remote secret entry")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("chat after remote form", func(view string) bool { return strings.Contains(view, "Normal chat remains available") })
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
		if fakes.MissingSecretFormMetadata(w, r, "GITHUB_TOKEN") {
			return
		}
		writes.Add(1)
		http.Error(w, value, http.StatusServiceUnavailable)
	}))
	defer api.Close()
	u, operations := fixtures.SecretFormUI(t, ctx, ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "commander", Password: "fixture-password"}}, value)
	defer u.Close()
	op := fixtures.SecretFormOperation("GITHUB_TOKEN")
	operations.Publish(op)
	u.Text("/watch " + op.ID)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("secret field", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	u.Text(value)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("unconfirmed save", func(view string) bool { return strings.Contains(view, "API write could not be confirmed") })
	if strings.Contains(u.Model.View(), value) || writes.Load() != 1 {
		t.Fatal("failed save disclosed the API response or retried automatically")
	}
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	target := operations.ReadCount() + 2
	u.Wait("updates after failed save", func(string) bool { return operations.ReadCount() >= target })
	if writes.Load() != 1 || strings.Contains(u.Model.View(), "Agent secrets") {
		t.Fatal("returning to chat retried or reopened the failed save")
	}
	u.Text("/secrets")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("reloaded form", func(view string) bool { return strings.Contains(view, "builder-agent · GITHUB_TOKEN") })
	if strings.Contains(u.Model.View(), strings.Repeat("•", len(value))) {
		t.Fatal("failed save value was retained in the reloaded form")
	}
	u.Key(tea.KeyMsg{Type: tea.KeyEsc})
	u.Text("Normal message after failed secret save")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("chat after failed save", func(view string) bool { return strings.Contains(view, "Normal chat remains available") })
}

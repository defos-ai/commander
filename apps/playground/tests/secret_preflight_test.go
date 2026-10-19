package playground_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

func TestOperationNeedsSecrets(t *testing.T) {
	const managed = `{"agent_type":"builder-agent","missing_requirements":[{"kind":"secret","name":"GITHUB_TOKEN"}]}`
	for _, test := range []struct {
		name, status, preflight string
		want                    bool
	}{
		{"blocked", "waiting", `{"actions":[{"unavailable_agents":[` + managed + `]}]}`, true},
		{"unknown status", "unknown", `{"actions":[{"unavailable_agents":[` + managed + `]}]}`, false},
		{"missing status", "", `{"actions":[{"unavailable_agents":[` + managed + `]}]}`, false},
		{"missing preflight", "waiting", `null`, false},
		{"ready", "planning", `{"ready":true,"actions":[{"unavailable_agents":[` + managed + `]}]}`, false},
		{"ready candidate", "waiting", `{"actions":[{"ready_agent_ids":["available"],"unavailable_agents":[` + managed + `]}]}`, false},
		{"mixed actions", "waiting", `{"actions":[{"ready_agent_ids":["available"],"unavailable_agents":[` + managed + `]},{"unavailable_agents":[` + managed + `]}]}`, true},
		{"mixed requirements", "waiting", `{"actions":[{"unavailable_agents":[{"agent_type":"builder-agent","missing_requirements":[{"kind":"secret","name":"GITHUB_TOKEN"},{"kind":"executable","name":"git"}]}]}]}`, true},
		{"sensitive environment", "waiting", `{"actions":[{"unavailable_agents":[{"agent_type":"builder-agent","missing_requirements":[{"kind":"configuration","name":"GITHUB_TOKEN","sensitive":true}]}]}]}`, false},
		{"stale candidate", "waiting", `{"actions":[{"unavailable_agents":[{"agent_type":"builder-agent","reason":"Agent heartbeat has expired"}]}]}`, false},
		{"missing scope", "waiting", `{"actions":[{"unavailable_agents":[{"missing_requirements":[{"kind":"secret","name":"GITHUB_TOKEN"}]}]}]}`, false},
		{"invalid scope", "waiting", `{"actions":[{"unavailable_agents":[{"agent_type":"../builder","missing_requirements":[{"kind":"secret","name":"GITHUB_TOKEN"}]}]}]}`, false},
		{"invalid name", "waiting", `{"actions":[{"unavailable_agents":[{"agent_type":"builder-agent","missing_requirements":[{"kind":"secret","name":"../TOKEN"},{"kind":"secret","name":""}]}]}]}`, false},
		{"completed", "completed", `{"actions":[{"unavailable_agents":[` + managed + `]}]}`, false},
		{"failed", "failed", `{"actions":[{"unavailable_agents":[` + managed + `]}]}`, false},
		{"cancelled", "cancelled", `{"actions":[{"unavailable_agents":[` + managed + `]}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var operation bridge.Operation
			if err := json.Unmarshal([]byte(`{"status":"`+test.status+`","preflight":`+test.preflight+`}`), &operation); err != nil {
				t.Fatal(err)
			}
			if got := operation.NeedsSecrets(); got != test.want {
				t.Fatalf("NeedsSecrets() = %v, want %v", got, test.want)
			}
			var want []secrets.Key
			if test.want {
				want = []secrets.Key{{Scope: secrets.Scope{AgentType: "builder-agent"}, Name: "GITHUB_TOKEN"}}
			}
			if got := operation.MissingSecrets(); !slices.Equal(got, want) {
				t.Fatalf("MissingSecrets() = %v, want %v", got, want)
			}
		})
	}
}

func TestOperationMissingSecretsDeduplicatesWithinScope(t *testing.T) {
	var operation bridge.Operation
	err := json.Unmarshal([]byte(`{"status":"waiting","preflight":{"actions":[
		{"unavailable_agents":[
			{"agent_type":"spawner-agent","missing_requirements":[{"kind":"secret","name":"GITHUB_TOKEN"}]},
			{"agent_type":"builder-agent","missing_requirements":[{"kind":"secret","name":"GITHUB_TOKEN"},{"kind":"secret","name":"CODEX_API_KEY"},{"kind":"secret","name":"../invalid"}]}
		]},
		{"unavailable_agents":[{"agent_type":"builder-agent","missing_requirements":[{"kind":"secret","name":"GITHUB_TOKEN"}]}]},
		{"ready_agent_ids":["ready-agent"],"unavailable_agents":[{"agent_type":"builder-agent","missing_requirements":[{"kind":"secret","name":"NOT_NEEDED"}]}]}
	]}}`), &operation)
	if err != nil {
		t.Fatal(err)
	}
	want := []secrets.Key{
		{Scope: secrets.Scope{AgentType: "builder-agent"}, Name: "CODEX_API_KEY"},
		{Scope: secrets.Scope{AgentType: "builder-agent"}, Name: "GITHUB_TOKEN"},
		{Scope: secrets.Scope{AgentType: "spawner-agent"}, Name: "GITHUB_TOKEN"},
	}
	if got := operation.MissingSecrets(); !slices.Equal(got, want) {
		t.Fatalf("missing secrets = %v, want ordered unique scoped keys %v", got, want)
	}
}

func TestSecretWaitAdminHandoff(t *testing.T) {
	for _, test := range []struct{ name, adminURL, destination string }{
		{"remote", "https://admin.example.test/commander/", "https://admin.example.test/commander/#/operations/blocked-operation"},
		{"local", "http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274&username=operator%2Blocal", "http://localhost:4275/#/connect?api=http%3A%2F%2Flocalhost%3A4274&operation=blocked-operation&username=operator%2Blocal"},
		{"unconfigured", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			u, publish, _ := adminOperationUI(t, ctx, test.adminURL, true)
			defer u.model.Close()
			u.model.Update(tea.WindowSizeMsg{Width: 300, Height: 80})
			op := bridge.Operation{
				ID: "blocked-operation", Status: "waiting", UpdatedAt: time.Now(),
				Preflight: &bridge.Preflight{Version: 1, Actions: []bridge.ActionReadiness{
					{UnavailableAgents: []bridge.AgentReadiness{{AgentType: "builder-agent", MissingRequirements: []actions.Requirement{{Kind: actions.SecretRequirement, Name: "GITHUB_TOKEN"}}}}},
				}},
			}
			publish(op)
			u.text("/watch " + op.ID)
			u.key(tea.KeyMsg{Type: tea.KeyEnter})
			u.wait("automatic secret form", func(view string) bool { return strings.Contains(view, "Connect to API") })
			u.key(tea.KeyMsg{Type: tea.KeyEsc})
			u.wait("secret wait progress", func(view string) bool {
				return strings.Contains(view, "secrets needed") && strings.Contains(view, "Operation reviewed") && !strings.Contains(view, "Thinking")
			})
			view := u.model.View()
			if strings.Contains(view, "requirements missing") {
				t.Fatal("managed secret wait still shows only generic requirements")
			}
			if test.destination != "" {
				if strings.Count(view, "Review on Admin UI ("+test.destination+")") != 1 {
					t.Fatal("secret wait did not preserve its operation-specific Admin destination")
				}
			} else if !strings.Contains(view, "secrets needed · /secrets · /admin for setup") || strings.Contains(view, "Review on Admin UI") || strings.Contains(view, "4275") {
				t.Fatal("unconfigured Admin hint invented a destination or omitted setup guidance")
			}

			// A fresh ready advertisement clears the handoff without another user
			// command or a claim that saving the value itself made the agent ready.
			op.Status, op.UpdatedAt, op.Preflight.Ready = "planning", op.UpdatedAt.Add(time.Second), true
			op.Preflight.Version++
			publish(op)
			u.wait("ready after secret installation", func(view string) bool {
				return strings.Contains(view, "blocked- · planning") && !strings.Contains(view, "secrets needed")
			})
			if strings.Contains(u.model.View(), "Review on Admin UI") {
				t.Fatal("secret wait review link remains after preflight is ready")
			}
		})
	}
}

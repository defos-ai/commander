package playground_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
	"github.com/defos-ai/commander/framework/host"
)

func TestProviderSetupShowsSharedSecretName(t *testing.T) {
	for _, provider := range llm.Presets() {
		t.Run(provider.ID, func(t *testing.T) {
			u := fixtures.NewProviderSetup(t, ui.Config{
				Provider: provider.ID, Model: "fixture", BaseURL: "http://127.0.0.1:1", NoColor: true,
				API: ui.Connection[host.BasicCredentials]{URL: "http://127.0.0.1:2", Credentials: host.BasicCredentials{
					Username: "commander", Password: fixtures.ProviderSecretPassword,
				}},
			}, io.Discard)

			u.Wait("provider key input", func(view string) bool { return strings.Contains(view, provider.Name+" API key") })

			view := u.Model.View()
			if !strings.Contains(view, "Saved encrypted as "+provider.KeyEnv[0]+" for agents if absent.") || strings.Contains(view, "Kept in memory for this session.") {
				t.Fatal("provider setup did not disclose the selected key's shared secret name")
			}
		})
	}
}

func TestProviderKeysSavedForAgents(t *testing.T) {
	for _, test := range []struct {
		name, provider, secret string
		entered                bool
	}{
		{"configured OpenAI", "openai", "OPENAI_API_KEY", false},
		{"entered OpenAI", "openai", "OPENAI_API_KEY", true},
		{"Anthropic", "anthropic", "ANTHROPIC_API_KEY", false},
		{"Gemini", "gemini", "GEMINI_API_KEY", false},
		{"xAI", "xai", "XAI_API_KEY", false},
		{"DeepSeek", "deepseek", "DEEPSEEK_API_KEY", false},
		{"compatible provider", "other", "COMMANDER_LLM_API_KEY", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			u, api := fixtures.ProviderSecretSetup(t, fixtures.ProviderSecretSetupOptions{
				Provider: test.provider, EnteredKey: test.entered, API: fakes.ProviderSecretOptions{Name: test.secret},
			})
			if test.entered {
				u.Wait("OpenAI key input", func(view string) bool { return strings.Contains(view, "OpenAI API key") })
				u.Text(fixtures.ProviderSecretValue)
				assertProviderSecretHidden(t, u.Model.View())
				u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			}

			u.Wait("shared provider key", func(view string) bool {
				return strings.Contains(view, test.secret+" saved for agents.")
			})
			u.Close()

			requests := api.Requests()
			if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodPost {
				t.Fatal("provider setup did not check metadata and create the missing key exactly once")
			}
			if requests[0].Path != "/api/v1/secrets/"+test.secret || requests[1].Name != test.secret || requests[1].Value != fixtures.ProviderSecretValue {
				t.Fatal("provider setup changed the shared secret name or value")
			}
			for _, request := range requests {
				if request.Username != "commander" || request.Password != fixtures.ProviderSecretPassword {
					t.Fatal("provider setup did not use the retained authenticated API connection")
				}
			}
			assertProviderSecretHidden(t, u.Model.View())
		})
	}
}

func TestProviderKeyPreservesSavedValue(t *testing.T) {
	for _, status := range []string{"available", "expired"} {
		t.Run(status, func(t *testing.T) {
			u, api := fixtures.ProviderSecretSetup(t, fixtures.ProviderSecretSetupOptions{API: fakes.ProviderSecretOptions{ExistingStatus: status}})

			u.Wait("existing shared key", func(view string) bool {
				return strings.Contains(view, "OPENAI_API_KEY is already saved; existing value kept.")
			})
			u.Close()

			requests := api.Requests()
			if len(requests) != 1 || requests[0].Method != http.MethodGet {
				t.Fatal("provider setup replaced an existing shared key")
			}
			assertProviderSecretHidden(t, u.Model.View())
		})
	}
}

func TestProviderKeyFailuresDoNotRetry(t *testing.T) {
	for _, test := range []struct {
		name     string
		options  fakes.ProviderSecretOptions
		requests int
	}{
		{"login rejected", fakes.ProviderSecretOptions{ReadStatus: http.StatusUnauthorized, ReadCode: "unauthorized"}, 1},
		{"secrets unavailable", fakes.ProviderSecretOptions{ReadStatus: http.StatusServiceUnavailable, ReadCode: "secrets_unavailable"}, 1},
		{"unrecognized missing response", fakes.ProviderSecretOptions{ReadStatus: http.StatusNotFound, ReadCode: "route_not_found"}, 1},
		{"uncertain write", fakes.ProviderSecretOptions{WriteStatus: http.StatusServiceUnavailable, WriteCode: "secret_write_uncertain"}, 2},
		{"concurrent creation", fakes.ProviderSecretOptions{WriteStatus: http.StatusConflict, WriteCode: "secret_conflict"}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			u, api := fixtures.ProviderSecretSetup(t, fixtures.ProviderSecretSetupOptions{API: test.options})

			u.Wait("local sharing failure", func(view string) bool {
				return strings.Contains(view, "OPENAI_API_KEY was not confirmed saved for agents:")
			})
			u.Close()

			requests := api.Requests()
			if len(requests) != test.requests {
				t.Fatalf("sharing failure made %d requests, want %d", len(requests), test.requests)
			}
			if test.requests == 1 && requests[0].Method != http.MethodGet {
				t.Fatal("unconfirmed absence permitted a secret write")
			}
			if strings.Contains(u.Model.View(), "OPENAI_API_KEY saved for agents.") {
				t.Fatal("failed sharing claimed success")
			}
			assertProviderSecretHidden(t, u.Model.View())
		})
	}
}

func TestEmptyOptionalProviderKeyIsNotShared(t *testing.T) {
	u, api := fixtures.ProviderSecretSetup(t, fixtures.ProviderSecretSetupOptions{Provider: "other", EnteredKey: true})
	u.Wait("optional provider key", func(view string) bool { return strings.Contains(view, "API key") })

	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("Commander connection", func(view string) bool { return strings.Contains(view, "Commander connected") })
	u.Close()

	if len(api.Requests()) != 0 {
		t.Fatal("empty optional provider key caused an API request or shared an unrelated provider key")
	}
	assertProviderSecretHidden(t, u.Model.View())
}

func TestProviderKeyWithoutAPILoginStaysSessionOnly(t *testing.T) {
	for _, test := range []struct {
		name    string
		options fixtures.ProviderSecretSetupOptions
	}{
		{"missing URL", fixtures.ProviderSecretSetupOptions{MissingAPIURL: true}},
		{"missing password", fixtures.ProviderSecretSetupOptions{MissingAPIPassword: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			u, api := fixtures.ProviderSecretSetup(t, test.options)

			u.Wait("session-only key guidance", func(view string) bool {
				return strings.Contains(view, "OPENAI_API_KEY is session-only.") && strings.Contains(view, "/admin secrets set OPENAI_API_KEY")
			})
			u.Close()

			if len(api.Requests()) != 0 || strings.Contains(u.Model.View(), "Connect to API") {
				t.Fatal("missing API login made a request or forced a login form")
			}
			assertProviderSecretHidden(t, u.Model.View())
		})
	}
}

func TestClosingPlaygroundCancelsProviderSecretSharing(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	u, api := fixtures.ProviderSecretSetup(t, fixtures.ProviderSecretSetupOptions{API: fakes.ProviderSecretOptions{BlockRead: true}})
	u.Wait("Commander connection", func(view string) bool { return strings.Contains(view, "Commander connected") })
	select {
	case <-api.ReadStarted():
	case <-ctx.Done():
		t.Fatal("provider sharing did not start its API request")
	}

	closed := make(chan struct{})
	go func() {
		u.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("closing Playground did not finish the pending secret worker")
	}

	select {
	case <-api.ReadCanceled():
	case <-ctx.Done():
		t.Fatal("closing Playground did not cancel the API request")
	}
	if requests := api.Requests(); len(requests) != 1 || requests[0].Method != http.MethodGet {
		t.Fatal("canceled sharing submitted a secret write")
	}
}

func assertProviderSecretHidden(t *testing.T, view string) {
	t.Helper()
	for _, private := range []string{fixtures.ProviderSecretValue, fixtures.ProviderSecretPassword, fixtures.UnrelatedProviderSecretValue} {
		if strings.Contains(view, private) {
			t.Fatal("provider credential sharing exposed a private value in the terminal")
		}
	}
}

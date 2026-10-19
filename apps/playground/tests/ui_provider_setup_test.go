package playground_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/framework/host"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProviderAuthenticationDuringSetup(t *testing.T) {
	const rejected = "rejected-provider-fixture-key"
	const accepted = "accepted-provider-fixture-key"
	for _, tc := range []struct {
		name       string
		status     int
		configured bool
		debug      bool
	}{
		{"entered-key", http.StatusUnauthorized, false, true},
		{"configured-key", http.StatusForbidden, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", rejected)
			server := mcp.NewServer(&mcp.Implementation{Name: "provider-setup-fixture", Version: "1"}, nil)
			commander := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
			defer commander.Close()
			var requests atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/custom/v1/models" {
					t.Error("provider setup changed the endpoint or submitted model work")
				}
				switch r.Header.Get("Authorization") {
				case "Bearer " + accepted:
					fmt.Fprint(w, `{"data":[{"id":"fixture-chat-model"}]}`)
				case "Bearer " + rejected:
					w.WriteHeader(tc.status)
					fmt.Fprintf(w, `{"error":{"message":"Incorrect API key: %s"}}`, rejected)
				default:
					t.Error("provider received a missing, stale or concatenated replacement key")
					w.WriteHeader(http.StatusUnauthorized)
				}
			}))
			defer endpoint.Close()
			config := ui.Config{Provider: "openai", BaseURL: endpoint.URL + "/custom/v1", MCP: ui.Connection[host.BearerCredentials]{URL: commander.URL}, NoColor: true, Debug: tc.debug}
			if tc.configured {
				config.Keys = map[string]string{"openai": rejected}
			}
			var output bytes.Buffer
			u := newProviderSetup(t, config, &output)
			defer u.model.Close()
			if !tc.configured {
				u.wait("API key input", providerKeyPrompt)
				if requests.Load() != 0 {
					t.Fatal("provider discovery started before a key was submitted")
				}
				u.text(rejected)
				assertProviderKeyHidden(t, u, &output, rejected, accepted)
				u.key(tea.KeyMsg{Type: tea.KeyEnter})
			}
			u.wait("API key retry", providerKeyPrompt)
			if requests.Load() != 1 {
				t.Fatal("initial authentication failure made unexpected requests")
			}
			if !strings.Contains(u.model.View(), "Get an API key: https://") {
				t.Fatal("API key retry omitted the provider's key creation link")
			}
			assertProviderKeyHidden(t, u, &output, rejected, accepted)
			u.key(tea.KeyMsg{Type: tea.KeyEnter})
			if requests.Load() != 1 || !providerKeyPrompt(u.model.View()) {
				t.Fatal("retry reused the rejected key or accepted an empty key")
			}
			for _, key := range []string{rejected, accepted} {
				before := requests.Load()
				u.text(key)
				assertProviderKeyHidden(t, u, &output, rejected, accepted)
				if !strings.Contains(u.model.View(), strings.Repeat("*", len(key))) || requests.Load() != before {
					t.Fatal("replacement key was not masked or submitted before Enter")
				}
				u.key(tea.KeyMsg{Type: tea.KeyEnter})
				if key == rejected {
					u.wait("retry after another rejected key", providerKeyPrompt)
					if strings.Count(u.model.View(), "API key rejected.") != 1 {
						t.Fatal("repeated authentication failure accumulated duplicate setup errors")
					}
				} else {
					u.wait("model selection after replacement key", func(view string) bool {
						return strings.Contains(view, "Select a model:") && strings.Contains(view, "fixture-chat-model")
					})
				}
				if requests.Load() != before+1 {
					t.Fatal("submitting a replacement key did not make exactly one discovery request")
				}
			}
			assertProviderSetupRecovered(t, u)
			u.key(tea.KeyMsg{Type: tea.KeyEnter})
			u.wait("chat after successful provider setup", func(view string) bool { return strings.Contains(view, "Commander connected") })
			assertProviderSetupRecovered(t, u)
			if requests.Load() != 3 || !strings.Contains(u.model.View(), "OpenAI · fixture-chat-model") {
				t.Fatal("successful setup changed the selected model or submitted unexpected model work")
			}
			assertProviderKeyHidden(t, u, &output, rejected, accepted)
			if os.Getenv("OPENAI_API_KEY") != rejected || (tc.configured && config.Keys["openai"] != rejected) {
				t.Fatal("interactive key replacement changed the environment or caller configuration")
			}
		})
	}
}

func assertProviderSetupRecovered(t *testing.T, u *uiDriver) {
	t.Helper()
	for _, stale := range []string{"API key rejected.", "An API key is required", "Incorrect API key:", "model API returned HTTP"} {
		if strings.Contains(u.model.View(), stale) {
			t.Fatal("resolved provider setup errors remained visible after successful authentication")
		}
	}
}

func TestProviderKeyRetryBack(t *testing.T) {
	const draft = "private-provider-key-draft"
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer endpoint.Close()
	u := newProviderSetup(t, ui.Config{
		Provider: "openai", BaseURL: endpoint.URL, Keys: map[string]string{"openai": "rejected-fixture-key"}, NoColor: true,
	}, io.Discard)
	u.wait("API key retry", providerKeyPrompt)
	u.text(draft)
	if strings.Contains(u.model.View(), draft) {
		t.Fatal("retry exposed the key draft")
	}
	u.key(tea.KeyMsg{Type: tea.KeyCtrlA})
	u.key(tea.KeyMsg{Type: tea.KeyCtrlK})
	u.text("/back")
	u.key(tea.KeyMsg{Type: tea.KeyEnter})
	view := u.model.View()
	if !strings.Contains(view, "Select your model provider:") || strings.Contains(view, draft) || requests.Load() != 1 {
		t.Fatal("back did not clear the key draft and return to provider selection without another request")
	}
}

func TestProviderDiscoveryManualFallback(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(status)
				if status == http.StatusOK {
					fmt.Fprint(w, `{"data":[]}`)
				}
			}))
			defer endpoint.Close()
			u := newProviderSetup(t, ui.Config{
				Provider: "openai", BaseURL: endpoint.URL, Keys: map[string]string{"openai": "fixture-key"}, NoColor: true,
			}, io.Discard)
			u.wait("manual URL fallback", func(view string) bool { return strings.Contains(view, "API base URL") })
			view := u.model.View()
			if !strings.Contains(view, endpoint.URL) || providerKeyPrompt(view) || requests.Load() != 1 {
				t.Fatal("non-authentication discovery failure lost its URL or incorrectly retried credentials")
			}
			u.key(tea.KeyMsg{Type: tea.KeyEnter})
			if !strings.Contains(u.model.View(), "Exact model name") || requests.Load() != 1 {
				t.Fatal("manual URL confirmation did not advance to model entry without rediscovery")
			}
		})
	}
}

func newProviderSetup(t *testing.T, config ui.Config, output io.Writer) *uiDriver {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	u := &uiDriver{t: t, ctx: ctx, messages: make(chan tea.Msg, 32), model: ui.New(ctx, config, output)}
	t.Cleanup(u.model.Close)
	u.model.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	u.run(u.model.Init())
	return u
}

func providerKeyPrompt(view string) bool {
	return strings.Contains(view, "OpenAI API key · Kept in memory for this session.")
}

func assertProviderKeyHidden(t *testing.T, u *uiDriver, output *bytes.Buffer, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if strings.Contains(u.model.View(), key) || strings.Contains(output.String(), key) {
			t.Fatal("provider key appeared in rendered setup or terminal output")
		}
	}
}

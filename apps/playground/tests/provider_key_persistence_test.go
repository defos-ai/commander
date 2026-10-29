package playground_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestProviderKeyRestoreWithoutNetwork(t *testing.T) {
	const cached = "private-cached-provider-key"
	const configured = "private-environment-provider-key"
	const endpoint = "https://custom-provider.example/v1"
	for _, test := range []struct {
		name, cached, configured string
		err                      error
		wantLoads                int
		prompt                   string
	}{
		{"cached", cached, "", nil, 1, "Commander MCP URL"},
		{"environment wins", cached, configured, nil, 0, "Commander MCP URL"},
		{"missing", "", "", nil, 1, "OpenAI API key"},
		{"cache error", cached, "", errors.New(cached), 1, "OpenAI API key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cache := fakes.NewProviderKeyCache(test.cached, test.err)
			var output bytes.Buffer
			u := fixtures.NewProviderSetup(t, ui.Config{
				Provider: "openai", Model: "fixture", BaseURL: endpoint, NoColor: true,
				Keys: map[string]string{"openai": test.configured}, LoadProviderKey: cache.Load, SaveProviderKey: cache.Save,
			}, &output)

			u.Wait("provider key restoration", func(view string) bool { return strings.Contains(view, test.prompt) })
			u.Close()

			loads := cache.Loads()
			if len(loads) != test.wantLoads {
				t.Fatal("provider key restoration did not honor environment precedence or attempted more than once")
			}
			if len(loads) == 1 && (loads[0].Provider != "openai" || loads[0].BaseURL != endpoint) {
				t.Fatal("restoration did not use the actual provider and custom endpoint")
			}
			if len(cache.Saves()) != 0 {
				t.Fatal("restored or configured provider key was written to the local cache")
			}
			assertProviderKeyHidden(t, u, &output, cached, configured)
			if strings.Contains(test.prompt, "API key") && !strings.Contains(u.Model.View(), "You can also set OPENAI_API_KEY before launch.") {
				t.Fatal("missing provider key omitted its canonical environment variable")
			}
		})
	}
}

func TestMissingProviderKeyShowsCanonicalEnvironment(t *testing.T) {
	for _, provider := range llm.Presets() {
		t.Run(provider.ID, func(t *testing.T) {
			cache := fakes.NewProviderKeyCache("", nil)
			u := fixtures.NewProviderSetup(t, ui.Config{
				Provider: provider.ID, Model: "fixture", BaseURL: "https://provider.example/v1", NoColor: true,
				LoadProviderKey: cache.Load, SaveProviderKey: cache.Save,
			}, io.Discard)

			u.Wait("provider key prompt", func(view string) bool { return strings.Contains(view, provider.Name+" API key") })

			if !strings.Contains(u.Model.View(), "You can also set "+provider.KeyEnv[0]+" before launch.") {
				t.Fatal("provider key prompt omitted its canonical environment name")
			}
			if !strings.Contains(u.Model.View(), "Remembered encrypted on this machine.") {
				t.Fatal("provider key prompt omitted local persistence behavior")
			}
		})
	}
}

func TestProviderURLChangeDoesNotReusePreviousKey(t *testing.T) {
	const previousURL = "invalid-provider-url"
	const nextURL = "https://another-provider.example/v1"
	const value = "private-key-for-previous-endpoint"
	for _, cached := range []bool{true, false} {
		t.Run(map[bool]string{true: "cached", false: "entered"}[cached], func(t *testing.T) {
			initial := ""
			if cached {
				initial = value
			}
			cache := fakes.NewBoundProviderKeyCache("openai", previousURL, initial)
			var output bytes.Buffer
			u := fixtures.NewProviderSetup(t, ui.Config{
				Provider: "openai", Model: "fixture", BaseURL: previousURL, NoColor: true,
				LoadProviderKey: cache.Load, SaveProviderKey: cache.Save,
			}, &output)
			if !cached {
				u.Wait("provider key entry", func(view string) bool { return strings.Contains(view, "OpenAI API key") })
				u.Text(value)
				u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			}
			u.Wait("manual provider URL", func(view string) bool { return strings.Contains(view, "API base URL") })

			u.Key(tea.KeyMsg{Type: tea.KeyCtrlA})
			u.Key(tea.KeyMsg{Type: tea.KeyCtrlK})
			u.Text(nextURL)
			u.Key(tea.KeyMsg{Type: tea.KeyEnter})
			u.Wait("new endpoint needs its own key", func(view string) bool { return strings.Contains(view, "OpenAI API key") })
			u.Close()

			loads := cache.Loads()
			if len(loads) != 2 || loads[0].BaseURL != previousURL || loads[1].BaseURL != nextURL {
				t.Fatal("changing provider URL did not perform a separate key lookup")
			}
			if len(cache.Saves()) != 0 {
				t.Fatal("changing provider URL cached a key for the previous endpoint")
			}
			assertProviderKeyHidden(t, u, &output, value)
		})
	}
}

func TestRejectedCachedProviderKeyIsReplacedAfterConnection(t *testing.T) {
	const rejected = "private-rejected-cached-key"
	const accepted = "private-accepted-entered-key"
	cache := fakes.NewProviderKeyCache(rejected, nil)
	provider := fakes.NewProviderKeyModel(accepted)
	var output bytes.Buffer
	u, endpoint := fixtures.ProviderKeyUI(t, cache, provider, "", &output)
	u.Wait("rejected cached provider key", func(view string) bool {
		return strings.Contains(view, "API key rejected.") && strings.Contains(view, "OpenAI API key")
	})
	loads := cache.Loads()
	if len(loads) != 1 || loads[0].Provider != "openai" || loads[0].BaseURL != endpoint {
		t.Fatal("cache restoration did not bind the key to the actual provider endpoint")
	}
	if len(cache.Saves()) != 0 {
		t.Fatal("rejected cache key was saved again")
	}
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(u.Model.View(), "An API key is required") {
		t.Fatal("empty retry reused the cached key")
	}

	u.Text(accepted)
	assertProviderKeyHidden(t, u, &output, rejected, accepted)
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("model selection", func(view string) bool { return strings.Contains(view, "Select a model:") })

	if len(cache.Loads()) != 0 || len(cache.Saves()) != 0 {
		t.Fatal("provider-key retry reloaded the rejected key or saved before Commander connected")
	}
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("Commander connection", func(view string) bool { return strings.Contains(view, "Commander connected") })
	u.Text("Hello")
	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("provider reply", func(view string) bool { return strings.Contains(view, "Provider key remains private") })
	u.Close()

	saves := cache.Saves()
	if len(saves) != 1 || saves[0].Provider != "openai" || saves[0].BaseURL != endpoint || saves[0].Value != accepted {
		t.Fatal("entered replacement was not saved once for the connected provider endpoint")
	}
	if len(cache.Loads()) != 0 {
		t.Fatal("rejected cache key was automatically loaded again")
	}
	requests := provider.Requests()
	if len(requests) != 3 || requests[2].Path != "/custom/v1/chat/completions" {
		t.Fatal("provider setup retried unexpectedly or omitted the chat request")
	}
	for _, request := range requests {
		if strings.Contains(request.Body, rejected) || strings.Contains(request.Body, accepted) {
			t.Fatal("provider credentials appeared in model context")
		}
	}
	assertProviderKeyHidden(t, u, &output, rejected, accepted)
}

func TestConfiguredProviderKeyIsNotCachedAfterConnection(t *testing.T) {
	const configured = "private-configured-provider-key"
	cache := fakes.NewProviderKeyCache("private-unused-cache-key", nil)
	provider := fakes.NewProviderKeyModel(configured)
	u, _ := fixtures.ProviderKeyUI(t, cache, provider, configured, io.Discard)
	u.Wait("model selection", func(view string) bool { return strings.Contains(view, "Select a model:") })

	u.Key(tea.KeyMsg{Type: tea.KeyEnter})
	u.Wait("Commander connection", func(view string) bool { return strings.Contains(view, "Commander connected") })
	u.Close()

	if len(cache.Loads()) != 0 || len(cache.Saves()) != 0 {
		t.Fatal("configured provider key triggered cache restoration or persistence")
	}
}

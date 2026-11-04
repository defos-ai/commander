package fixtures

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/framework/host"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Playground struct {
	mu        sync.Mutex
	view      string
	views     []string
	histories [][]byte
	savedKeys int
	program   *tea.Program
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

func NewTransientPlayground(t *testing.T, fake *fakes.TransientSecrets) *Playground {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(fake.API))
	t.Cleanup(api.Close)
	provider := httptest.NewServer(http.HandlerFunc(fake.Model))
	t.Cleanup(provider.Close)
	server := fake.MCP()
	mcpHTTP := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(mcpHTTP.Close)
	ctx, cancel := context.WithCancel(context.Background())
	playground := &Playground{cancel: cancel, done: make(chan struct{})}
	model := ui.New(ctx, ui.Config{
		Provider: "other", Model: "fixture-model", BaseURL: provider.URL, NoColor: true,
		Keys: map[string]string{"other": "fixture-model-key"},
		API:  ui.Connection[host.BasicCredentials]{URL: api.URL, Credentials: host.BasicCredentials{Username: "operator", Password: "fixture-api-password"}},
		MCP:  ui.Connection[host.BearerCredentials]{URL: mcpHTTP.URL},
		SaveHistory: func(_ string, history ui.History) error {
			data, err := json.Marshal(history)
			playground.mu.Lock()
			defer playground.mu.Unlock()
			playground.histories = append(playground.histories, data)
			return err
		},
		SaveProviderKey: func(_, _, _ string) error {
			playground.mu.Lock()
			defer playground.mu.Unlock()
			playground.savedKeys++
			return nil
		},
	}, io.Discard)
	playground.program = tea.NewProgram(&observedModel{model: model, observed: playground}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler(), tea.WithContext(ctx))
	go func() {
		defer close(playground.done)
		_, _ = playground.program.Run()
		_ = model.Close()
	}()
	t.Cleanup(playground.Close)
	playground.program.Send(tea.WindowSizeMsg{Width: 150, Height: 45})
	return playground
}

func (p *Playground) Close() { p.closeOnce.Do(func() { p.cancel(); <-p.done }) }

func (p *Playground) Key(key tea.KeyType) { p.program.Send(tea.KeyMsg{Type: key}) }
func (p *Playground) Text(text string) {
	p.program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
}
func (p *Playground) Command(command string) { p.Text(command); p.Key(tea.KeyEnter) }

func (p *Playground) View() string { p.mu.Lock(); defer p.mu.Unlock(); return p.view }

func (p *Playground) Evidence() ([]string, [][]byte, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	history := make([][]byte, len(p.histories))
	for i, data := range p.histories {
		history[i] = append([]byte(nil), data...)
	}
	return append([]string(nil), p.views...), history, p.savedKeys
}

func (p *Playground) Await(ctx context.Context, matches func(string) bool) (string, error) {
	timer := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()
	for {
		view := p.View()
		if matches(view) {
			return view, nil
		}
		select {
		case <-ctx.Done():
			return view, ctx.Err()
		case <-timer.C:
		}
	}
}

type observedModel struct {
	model    tea.Model
	observed *Playground
}

func (m *observedModel) Init() tea.Cmd { return m.model.Init() }
func (m *observedModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.model.Update(message)
	m.model = next
	view := ansi.Strip(next.View())
	m.observed.mu.Lock()
	m.observed.view = view
	m.observed.views = append(m.observed.views, view)
	m.observed.mu.Unlock()
	return m, cmd
}
func (m *observedModel) View() string { return m.model.View() }
